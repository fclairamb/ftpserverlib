// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
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
	listener, err := net.Listen("tcp", ":20") //nolint:gosec
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
