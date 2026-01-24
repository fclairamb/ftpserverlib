package ftpserver

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

var (
	errClosedConn     = net.ErrClosed
	errConnReset      = errors.New("connection reset by peer")
	errOther          = errors.New("some other error")
	errWrappedClosed  = fmt.Errorf("failed: %w", errClosedConn)
	errClosedConnText = errors.New("use of closed network connection")
	errRandomError    = errors.New("some random error")
)

func TestIsClosedConnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "net.ErrClosed", err: errClosedConn, expected: true},
		{name: "connection reset by peer", err: errConnReset, expected: true},
		{name: "wrapped closed connection error", err: errWrappedClosed, expected: true},
		{name: "other error", err: errOther, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := isClosedConnError(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// mockNetError implements net.Error for testing
type mockNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *mockNetError) Error() string   { return e.msg }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

// testHandleCommandsStreamErrorCase represents a test case for handleCommandsStreamError.
type testHandleCommandsStreamErrorCase struct {
	name               string
	err                error
	debug              bool
	expectedDisconnect bool
}

// getHandleCommandsStreamErrorTestCases returns all test cases for handleCommandsStreamError.
func getHandleCommandsStreamErrorTestCases() []testHandleCommandsStreamErrorCase {
	return []testHandleCommandsStreamErrorCase{
		{name: "EOF with debug", err: io.EOF, debug: true, expectedDisconnect: true},
		{name: "EOF without debug", err: io.EOF, debug: false, expectedDisconnect: true},
		{name: "non-net.Error closed with debug", err: errClosedConnText, debug: true, expectedDisconnect: true},
		{name: "non-net.Error closed without debug", err: errClosedConnText, debug: false, expectedDisconnect: true},
		{name: "non-net.Error reset", err: errConnReset, debug: true, expectedDisconnect: true},
		{name: "net.Error closed with debug", err: &mockNetError{msg: "use of closed network connection"},
			debug: true, expectedDisconnect: true},
		{name: "net.Error closed without debug", err: &mockNetError{msg: "use of closed network connection"},
			debug: false, expectedDisconnect: true},
		{name: "net.Error other", err: &mockNetError{msg: "other network error"}, debug: false, expectedDisconnect: true},
		{name: "generic error", err: errRandomError, debug: false, expectedDisconnect: true},
	}
}

// TestHandleCommandsStreamError tests the handleCommandsStreamError function
// with various error types to ensure proper handling of closed connections.
func TestHandleCommandsStreamError(t *testing.T) {
	t.Parallel()

	//nolint:sloglint // DiscardHandler requires Go 1.23+
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, testCase := range getHandleCommandsStreamErrorTestCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			buf := bytes.Buffer{}
			server := &FtpServer{
				settings: &Settings{IdleTimeout: 0},
				Logger:   logger,
			}
			handler := &clientHandler{
				writer: bufio.NewWriter(&buf),
				server: server,
				logger: logger,
				debug:  testCase.debug,
			}

			result := handler.handleCommandsStreamError(testCase.err)
			assert.Equal(t, testCase.expectedDisconnect, result)
		})
	}
}

func TestDeflateReadWriterFlush(t *testing.T) {
	t.Parallel()

	// Create a buffer to write to
	buf := &mockReadWriter{}

	// Create deflate transfer
	deflate, err := newDeflateTransfer(buf, 5)
	require.NoError(t, err)

	// Write some data
	data := []byte("test data for deflate")
	n, err := deflate.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	// Test Flush
	err = deflate.Flush()
	require.NoError(t, err)

	// Verify data was written to buffer
	require.Positive(t, buf.writeCount)
}

func TestNewDeflateTransferInvalidLevel(t *testing.T) {
	t.Parallel()

	buf := &mockReadWriter{}

	// Test with invalid compression level (valid range is -2 to 9)
	_, err := newDeflateTransfer(buf, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not create deflate writer")
}

// mockReadWriter is a simple mock for testing
type mockReadWriter struct {
	writeCount int
	readCount  int
}

func (m *mockReadWriter) Write(p []byte) (int, error) {
	m.writeCount++

	return len(p), nil
}

func (m *mockReadWriter) Read(_ []byte) (int, error) {
	m.readCount++

	return 0, nil
}

// TestImmediateClientDisconnect tests that when a client connects and immediately
// disconnects (before sending any FTP commands), the server handles it gracefully
// without logging errors. This is common behavior for FTP clients that probe
// connections or do quick connectivity checks.
func TestImmediateClientDisconnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		debug bool
	}{
		{name: "with debug", debug: true},
		{name: "without debug", debug: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := NewTestServer(t, testCase.debug)
			dialer := &net.Dialer{Timeout: 5 * time.Second}

			// Connect and immediately close without sending any commands
			conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
			require.NoError(t, err)

			// Read the welcome message to ensure the server has started handling us
			buf := make([]byte, 1024)
			_, err = conn.Read(buf)
			require.NoError(t, err)

			// Close immediately without sending any commands
			err = conn.Close()
			require.NoError(t, err)

			// Give the server time to process the disconnect
			time.Sleep(100 * time.Millisecond)

			// Verify server is still functional by connecting again
			newConn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
			require.NoError(t, err)

			defer func() { _ = newConn.Close() }()

			_, err = newConn.Read(buf)
			require.NoError(t, err)
			require.Contains(t, string(buf), "220")
		})
	}
}

// TestMultipleImmediateDisconnects tests that the server handles many rapid
// connect/disconnect cycles gracefully (simulating probe traffic).
func TestMultipleImmediateDisconnects(t *testing.T) {
	t.Parallel()

	server := NewTestServer(t, true)
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	for range 10 {
		conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
		require.NoError(t, err)

		// Read welcome message
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)

		// Immediate close
		_ = conn.Close()
	}

	// Small delay to let server process all disconnects
	time.Sleep(200 * time.Millisecond)

	// Verify server still works
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	_, err = client.ReadDir("/")
	require.NoError(t, err)
}
