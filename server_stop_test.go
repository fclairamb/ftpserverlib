package ftpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestServerStopDoesNotLogError tests that stopping a server doesn't log an error
// when the listener is closed as expected
func TestServerStopDoesNotLogError(t *testing.T) {
	req := require.New(t)

	// Create a server with a test driver
	server := NewFtpServer(&TestServerDriver{
		Settings: &Settings{
			ListenAddr: "127.0.0.1:0", // Use dynamic port
		},
	})

	// Use a custom logger that tracks error logs
	mockHandler := &MockLogHandler{}
	server.Logger = slog.New(mockHandler)

	// Start listening
	err := server.Listen()
	req.NoError(err)

	// Start serving in a goroutine
	var serveErr error
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()
		serveErr = server.Serve()
	}()

	// Give the server a moment to start accepting connections
	time.Sleep(100 * time.Millisecond)

	// Stop the server
	err = server.Stop()
	req.NoError(err)

	// Wait for the Serve goroutine to finish
	waitGroup.Wait()

	// Serve should return nil (no error) when stopped normally
	req.NoError(serveErr)

	// Check that no error was logged for the "use of closed network connection"
	// The mock logger should not have received any error logs
	req.Empty(mockHandler.ErrorLogs, "Expected no error logs when stopping server, but got: %v", mockHandler.ErrorLogs)
}

// MockLogHandler captures log calls to verify behavior
type MockLogHandler struct {
	ErrorLogs []string
	WarnLogs  []string
	InfoLogs  []string
	DebugLogs []string
	mu        sync.Mutex
}

func (m *MockLogHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

//nolint:gocritic // slog.Handler interface requires value receiver
func (m *MockLogHandler) Handle(_ context.Context, record slog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch record.Level {
	case slog.LevelDebug:
		m.DebugLogs = append(m.DebugLogs, record.Message)
	case slog.LevelInfo:
		m.InfoLogs = append(m.InfoLogs, record.Message)
	case slog.LevelWarn:
		m.WarnLogs = append(m.WarnLogs, record.Message)
	case slog.LevelError:
		m.ErrorLogs = append(m.ErrorLogs, record.Message)
	}

	return nil
}

func (m *MockLogHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return m
}

func (m *MockLogHandler) WithGroup(_ string) slog.Handler {
	return m
}

var errTestListenerClose = errors.New("listener close failed")

type failingCloseListener struct {
	net.Listener
}

func (l *failingCloseListener) Close() error {
	return errors.Join(l.Listener.Close(), errTestListenerClose)
}

// newMultiplexingTestServer listens on listener with a multiplexed passive port reserved and returns that port
func newMultiplexingTestServer(t *testing.T, listener net.Listener) (*FtpServer, int) {
	t.Helper()

	server := NewFtpServer(&TestServerDriver{Settings: &Settings{
		Listener:                        listener,
		PassiveTransferPortRange:        &PortRange{Start: 0, End: 0},
		PassiveTransferPortMultiplexing: true,
	}})
	server.Logger = slog.New(slog.DiscardHandler)
	require.NoError(t, server.Listen())
	t.Cleanup(func() { _ = server.passiveListeners.close() })

	_, reservation, _, err := server.passiveListeners.reserve(net.ParseIP("127.0.0.1"), &PortRange{Start: 0, End: 0})
	require.NoError(t, err)

	addr, ok := reservation.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return server, addr.Port
}

func requirePortReleased(t *testing.T, port int) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", fmt.Sprintf("0.0.0.0:%d", port))
	require.NoError(t, err, "port %d should be released", port)
	require.NoError(t, listener.Close())
}

func TestServerStopClosesPassiveListenersWhenListenerCloseFails(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	require.NoError(t, err)

	server, port := newMultiplexingTestServer(t, &failingCloseListener{Listener: listener})

	require.ErrorIs(t, server.Stop(), errTestListenerClose)
	requirePortReleased(t, port)
}

func TestServerStopClosesPassiveListenersAfterListenerClosed(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	require.NoError(t, err)

	server, port := newMultiplexingTestServer(t, listener)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve() }()

	require.NoError(t, listener.Close())
	require.NoError(t, <-serveErr)

	require.ErrorIs(t, server.Stop(), ErrNotListening)
	requirePortReleased(t, port)
}

func TestServerStopBeforeListen(t *testing.T) {
	server := NewFtpServer(&TestServerDriver{Settings: &Settings{ListenAddr: testServerListenAddr}})

	require.ErrorIs(t, server.Stop(), ErrNotListening)
}

func TestServerStopLogsPassiveListenersCloseFailure(t *testing.T) {
	mockHandler := &MockLogHandler{}
	server := NewFtpServer(&TestServerDriver{Settings: &Settings{
		ListenAddr:                      testServerListenAddr,
		PassiveTransferPortRange:        &PortRange{Start: 0, End: 0},
		PassiveTransferPortMultiplexing: true,
	}})
	server.Logger = slog.New(mockHandler)
	require.NoError(t, server.Listen())

	// The shared passive listener still closes its port, but reports a failure
	server.passiveListeners.wrap = func(listener net.Listener) (net.Listener, error) {
		return &failingCloseListener{Listener: listener}, nil
	}

	_, reservation, _, err := server.passiveListeners.reserve(net.ParseIP("127.0.0.1"), &PortRange{Start: 0, End: 0})
	require.NoError(t, err)

	addr, ok := reservation.Addr().(*net.TCPAddr)
	require.True(t, ok)

	require.NoError(t, server.Stop())
	requirePortReleased(t, addr.Port)

	mockHandler.mu.Lock()
	defer mockHandler.mu.Unlock()

	require.Contains(t, mockHandler.WarnLogs, "Could not close passive listeners")
}
