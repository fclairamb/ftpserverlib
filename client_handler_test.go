package ftpserver

import (
	"crypto/tls"
	"fmt"
	"net"
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

	for i := 0; i < nbClients; i++ {
		go func() {
			conf := goftp.Config{
				User:     authUser,
				Password: authPass,
			}

			var err error
			var c *goftp.Client

			if c, err = goftp.DialConfig(conf, server.Addr()); err != nil {
				panic(fmt.Sprintf("Couldn't connect: %v", err))
			}

			if _, err = c.ReadDir("/"); err != nil {
				panic(fmt.Sprintf("Couldn't list dir: %v", err))
			}

			defer func() { panicOnError(c.Close()) }()

			waitGroup.Done()
		}()
	}

	waitGroup.Wait()
}

func TestDOS(t *testing.T) {
	s := NewTestServer(t, true)
	conn, err := net.DialTimeout("tcp", s.Addr(), 5*time.Second)
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:n])
	require.Equal(t, "220 TEST Server\r\n", response)

	written := 0

	for {
		n, err = conn.Write([]byte("some text without line ending"))
		written += n

		if err != nil {
			break
		}

		if written > 4096 {
			s.Logger.Warn("test DOS",
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
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// we send STOR without opening a transfer connection
	rc, response, err := raw.SendCommand("STOR file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, "unable to open transfer: no transfer connection", response)
}

func TestTLSMethods(t *testing.T) {
	t.Run("without-tls", func(t *testing.T) {
		cc := clientHandler{
			server: NewTestServer(t, true),
		}
		require.False(t, cc.HasTLSForControl())
		require.False(t, cc.HasTLSForTransfers())
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		s := NewTestServerWithDriver(t, &TestServerDriver{
			Settings: &Settings{
				TLSRequired: ImplicitEncryption,
			},
			TLS:   true,
			Debug: true,
		})
		cc := clientHandler{
			server: s,
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
	s := NewTestServerWithDriver(t, driver)

	conn, err := net.DialTimeout("tcp", s.Addr(), 5*time.Second)
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
		Debug: true,
	}
	s := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	ftpUpload(t, c, createTemporaryFile(t, 1024*1024), "file.bin")

	require.Len(t, driver.GetClientsInfo(), 1)

	err = c.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := c.OpenRawConn()
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
	s := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	done := make(chan bool, 1)
	connected := make(chan bool, 1)

	go func() {
		_, err := c.Getwd()
		assert.NoError(t, err)
		connected <- true

		counter := 0

		for counter < 100 {
			_, err := c.Getwd()
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
	for _, c := range list {
		if s == c {
			return true
		}
	}

	return false
}

func TestUnknownCommand(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
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

func (*testNetConn) Read(b []byte) (n int, err error) {
	return
}

func (*testNetConn) Write(b []byte) (n int, err error) {
	return
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

func (*testNetConn) SetDeadline(t time.Time) error {
	return nil
}

func (*testNetConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (*testNetConn) SetWriteDeadline(t time.Time) error {
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
	controlConnIP := net.ParseIP("192.168.1.1")

	c := clientHandler{
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

	err := c.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	assert.NoError(t, err) // ip match

	err = c.checkDataConnectionRequirement(net.ParseIP("192.168.1.2"), DataChannelActive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "does not match control connection ip address")
	}

	c.conn = &testNetConn{
		remoteAddr: &net.IPAddr{IP: controlConnIP},
	}

	err = c.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	assert.Error(t, err)

	// nil remote address
	c.conn = &testNetConn{}
	err = c.checkDataConnectionRequirement(controlConnIP, DataChannelActive)
	assert.Error(t, err)

	// invalid IP
	c.conn = &testNetConn{
		remoteAddr: &net.TCPAddr{IP: nil, Port: 21},
	}

	err = c.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid remote IP")
	}

	// invalid setting
	c.server.settings.PasvConnectionsCheck = 100
	err = c.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "unhandled data connection requirement")
	}
}

func TestDataConnectionWithTLSAllowed(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug:                true,
		TLS:                  true,
		ConnectionCheckReply: connectionCheckDataSecure,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			// nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("EPSV")
	require.NoError(t, err)
	require.Equal(t, StatusEnteringEPSV, rc)
}

func TestDataConnectionWithTLSInitiallyThenPlainTextFails(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug:                true,
		TLS:                  true,
		ConnectionCheckReply: connectionCheckDataSecure,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, response, err := raw.SendCommand("PROT C")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "OK", response)

	rc, resp, err := raw.SendCommand("EPSV")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, rc)
	require.Contains(t, resp, "connection must be secure")
}

func TestDataConnectionWithoutTLSFails(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug:                true,
		TLS:                  true,
		ConnectionCheckReply: connectionCheckDataSecure,
		Settings: &Settings{
			ActiveConnectionsCheck: IPMatchDisabled,
		},
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// passive connection
	rc, resp, err := raw.SendCommand("EPSV")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, rc)
	require.Contains(t, resp, "connection must be secure")

	// active connection
	rc, resp, err = raw.SendCommand("EPRT |1|::1|2000|")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, rc)
	require.Contains(t, resp, "connection must be secure")
}
