package ftpserver

import (
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrency(t *testing.T) {
	server := NewTestServer(t, false)

	nbClients := 100

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(nbClients)

	for range nbClients {
		go func() {
			conf := goftp.Config{
				User:     authUser,
				Password: authPass,
			}

			client, err := goftp.DialConfig(conf, server.Addr())
			if err != nil {
				panic(fmt.Sprintf("Couldn't connect: %v", err))
			}

			if _, err = client.ReadDir("/"); err != nil {
				panic(fmt.Sprintf("Couldn't list dir: %v", err))
			}

			defer func() { panicOnError(client.Close()) }()

			waitGroup.Done()
		}()
	}

	waitGroup.Wait()
}

func TestDOS(t *testing.T) {
	server := NewTestServer(t, true)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 128)
	readBytes, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:readBytes])
	require.Equal(t, "220 TEST Server\r\n", response)

	written := 0

	for {
		readBytes, err = conn.Write([]byte("some text without line ending"))
		written += readBytes

		if err != nil {
			break
		}

		if written > 4096 {
			server.Logger.Warn("test DOS",
				"bytes written", written)
		}
	}
}

func TestLastCommand(t *testing.T) {
	cc := clientHandler{}
	assert.Empty(t, cc.GetLastCommand())
}

func TestLastDataChannel(t *testing.T) {
	cc := clientHandler{lastDataChannel: DataChannelPassive}
	assert.Equal(t, DataChannelPassive, cc.GetLastDataChannel())
}

func TestTransferOpenError(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// we send STOR without opening a transfer connection
	rc, response, err := raw.SendCommand("STOR file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, "unable to open transfer: no transfer connection", response)
}

func TestTLSMethods(t *testing.T) {
	t.Parallel()

	t.Run("without-tls", func(t *testing.T) {
		t.Parallel()
		cc := clientHandler{
			server: NewTestServer(t, false),
		}
		require.False(t, cc.HasTLSForControl())
		require.False(t, cc.HasTLSForTransfers())
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		t.Parallel()
		server := NewTestServerWithTestDriver(t, &TestServerDriver{
			Settings: &Settings{
				TLSRequired: ImplicitEncryption,
			},
			TLS:   true,
			Debug: false,
		})
		cc := clientHandler{
			server: server,
		}
		require.True(t, cc.HasTLSForControl())
		require.True(t, cc.HasTLSForTransfers())
	})
}

func TestConnectionNotAllowed(t *testing.T) {
	driver := &TestServerDriver{
		Debug:          true,
		CloseOnConnect: true,
	}
	s := NewTestServerWithTestDriver(t, driver)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", s.Addr())
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:n])
	require.Equal(t, "500 TEST Server\r\n", response)

	_, err = conn.Write([]byte("NOOP\r\n"))
	require.NoError(t, err)

	_, err = conn.Read(buf)
	require.Error(t, err)
}

func TestCloseConnection(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	ftpUpload(t, client, createTemporaryFile(t, 1024*1024), "file.bin")

	require.Len(t, driver.GetClientsInfo(), 1)

	err = client.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	require.Len(t, driver.GetClientsInfo(), 2)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 1
	}, 1*time.Second, 50*time.Millisecond)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 0
	}, 1*time.Second, 50*time.Millisecond)
}

func TestClientContextConcurrency(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	done := make(chan bool, 1)
	connected := make(chan bool, 1)

	go func() {
		_, err := client.Getwd()
		assert.NoError(t, err)
		connected <- true

		counter := 0

		for counter < 100 {
			_, err := client.Getwd()
			assert.NoError(t, err)

			counter++
		}

		done <- true
	}()

	<-connected

	isDone := false
	for !isDone {
		info := driver.GetClientsInfo()
		assert.Len(t, info, 1)

		select {
		case <-done:
			isDone = true
		default:
		}
	}
}

type multilineMessage struct {
	message       string
	expectedLines []string
}

func TestMultiLineMessages(t *testing.T) {
	testMultilines := []multilineMessage{
		{
			message:       "single line",
			expectedLines: []string{"single line"},
		},
		{
			message:       "",
			expectedLines: []string{""},
		},
		{
			message:       "first line\r\nsecond line\r\n",
			expectedLines: []string{"first line", "second line"},
		},
		{
			message:       "first line\nsecond line\n",
			expectedLines: []string{"first line", "second line"},
		},
		{
			message:       "first line\rsecond line",
			expectedLines: []string{"first line\rsecond line"},
		},
		{
			message: `first line

second line

`,
			expectedLines: []string{"first line", "", "second line", ""},
		},
	}

	for _, msg := range testMultilines {
		lines := getMessageLines(msg.message)
		if len(lines) != len(msg.expectedLines) {
			t.Errorf("unexpected number of lines got: %v want: %v", len(lines), len(msg.expectedLines))
		}

		for _, line := range lines {
			if !isStringInSlice(line, msg.expectedLines) {
				t.Errorf("unexpected line %#v", line)
			}
		}
	}
}

func isStringInSlice(s string, list []string) bool {
	return slices.Contains(list, s)
}

func TestUnknownCommand(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	cmd := "UNSUPPORTED"
	rc, response, err := raw.SendCommand(cmd)
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
	require.Equal(t, fmt.Sprintf("Unknown command %#v", cmd), response)
}

// testNetConn implements net.Conn interface
type testNetConn struct {
	remoteAddr net.Addr
}

func (*testNetConn) Read(_ []byte) (int, error) {
	return 0, nil
}

func (*testNetConn) Write(_ []byte) (int, error) {
	return 0, nil
}

func (*testNetConn) Close() error {
	return nil
}

func (*testNetConn) LocalAddr() net.Addr {
	return nil
}

func (c *testNetConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (*testNetConn) SetDeadline(_ time.Time) error {
	return nil
}

func (*testNetConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (*testNetConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

// testNetListener implements net.Listener interface
type testNetListener struct {
	conn net.Conn
}

func (l *testNetListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		return l.conn, nil
	}

	return nil, &net.AddrError{}
}

func (*testNetListener) Close() error {
	return nil
}

func (*testNetListener) Addr() net.Addr {
	return nil
}

func TestDataConnectionRequirements(t *testing.T) {
	req := require.New(t)
	controlConnIP := net.ParseIP("192.168.1.1")

	cltHandler := clientHandler{
		conn: &testNetConn{
			remoteAddr: &net.TCPAddr{IP: controlConnIP, Port: 21},
		},
		server: &FtpServer{
			settings: &Settings{
				PasvConnectionsCheck:   IPMatchRequired,
				ActiveConnectionsCheck: IPMatchRequired,
			},
		},
	}

	err := cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	req.NoError(err) // ip match

	err = cltHandler.checkDataConnectionRequirement(net.ParseIP("192.168.1.2"), DataChannelActive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "does not match control connection ip address")
	}

	cltHandler.conn = &testNetConn{
		remoteAddr: &net.IPAddr{IP: controlConnIP},
	}

	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	req.Error(err)

	// nil remote address
	cltHandler.conn = &testNetConn{}
	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelActive)
	req.Error(err)

	// invalid IP
	cltHandler.conn = &testNetConn{
		remoteAddr: &net.TCPAddr{IP: nil, Port: 21},
	}

	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid remote IP")
	}

	// invalid setting
	cltHandler.server.settings.PasvConnectionsCheck = 100
	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "unhandled data connection requirement")
	}
}

func TestExtraData(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	info := driver.GetClientsInfo()
	require.Len(t, info, 1)

	for k, v := range info {
		ccInfo, ok := v.(map[string]any)
		require.True(t, ok)
		extra, ok := ccInfo["extra"].(uint32)
		require.True(t, ok)
		require.Equal(t, k, extra)
	}
}
