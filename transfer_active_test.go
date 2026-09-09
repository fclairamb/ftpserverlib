// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"io"
	"net"
	"regexp"
	"testing"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func testRegexMatch(t *testing.T, regexp *regexp.Regexp, strings []string, expectedMatch bool) {
	t.Helper()

	for _, s := range strings {
		if regexp.MatchString(s) != expectedMatch {
			t.Errorf("Invalid match result: %s", s)
		}
	}
}

func TestRemoteAddrFormat(t *testing.T) {
	testRegexMatch(t, remoteAddrRegex, []string{"1,2,3,4,5,6"}, true)
	testRegexMatch(t, remoteAddrRegex, []string{"1,2,3,4,5"}, false)
}

func TestActiveTransferFromPort20(t *testing.T) {
	lc := &net.ListenConfig{}
	listener, err := lc.Listen(t.Context(), "tcp", ":20")
	if err != nil {
		t.Skipf("Binding on port 20 is not supported here: %v", err)
	}

	err = listener.Close()
	require.NoError(t, err)

	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		Settings: &Settings{
			ActiveTransferPortNon20: false,
		},
	})

	conf := goftp.Config{
		User:            authUser,
		Password:        authPass,
		ActiveTransfers: true,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.ReadDir("/")
	require.NoError(t, err)

	// the second ReadDir fails if we don't se the SO_REUSEPORT/SO_REUSEADDR socket options
	_, err = client.ReadDir("/")
	require.NoError(t, err)
}

func TestActiveTransferLocalIPResolver(t *testing.T) {
	// The address the server is asked to dial from has to exist on this host. Every 127/8 address
	// does on Linux, not everywhere, so the test steps aside where it cannot be bound.
	const sourceIP = "127.0.0.2"

	lc := &net.ListenConfig{}

	probe, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort(sourceIP, "0"))
	if err != nil {
		t.Skipf("Binding on %s is not supported here: %v", sourceIP, err)
	}

	require.NoError(t, probe.Close())

	resolvedFor := make(chan string, 1)
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Settings: &Settings{
			ActiveTransferPortNon20: true,
			ActiveTransferLocalIPResolver: func(cc ClientContext) net.IP {
				resolvedFor <- cc.LocalAddr().String()

				return net.ParseIP(sourceIP)
			},
		},
	})

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// We play the client side of the data connection ourselves, to see which address dials in
	dataListener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { require.NoError(t, dataListener.Close()) }()

	dataAddr, ok := dataListener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	rc, response, err := raw.SendCommand("PORT 127,0,0,1,%d,%d", dataAddr.Port/256, dataAddr.Port%256)
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	rc, response, err = raw.SendCommand("LIST")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, rc, response)

	dataConn, err := dataListener.Accept()
	require.NoError(t, err)

	peer, ok := dataConn.RemoteAddr().(*net.TCPAddr)
	require.True(t, ok)
	require.Equal(t, sourceIP, peer.IP.String(), "the data connection must come from the resolved address")

	_, err = io.Copy(io.Discard, dataConn)
	require.NoError(t, err)
	require.NoError(t, dataConn.Close())

	rc, response, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, rc, response)

	// The resolver saw the control connection, whose local address is the server's listening address
	require.Equal(t, server.Addr(), <-resolvedFor)
}
