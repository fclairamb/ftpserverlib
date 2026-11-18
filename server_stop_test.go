package ftpserver

import (
	"context"
	"errors"
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

var errTestListenerClose = errors.New("listener close failed")

type failingCloseListener struct {
	net.Listener
}

func (l *failingCloseListener) Close() error {
	return errors.Join(l.Listener.Close(), errTestListenerClose)
}

// acceptNotifyListener closes accepting when Serve first calls Accept. With a gate, a failed Accept returns only once
// gate is closed, which holds Serve before it handles the error.
type acceptNotifyListener struct {
	net.Listener
	once      sync.Once
	accepting chan struct{}
	gate      chan struct{}
}

func (l *acceptNotifyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })

	conn, err := l.Listener.Accept()
	if err != nil && l.gate != nil {
		<-l.gate
	}

	return conn, err
}

// reservePassivePort reserves a multiplexed passive port, which the system picks, for remoteIP and returns it
func reservePassivePort(t *testing.T, server *FtpServer, remoteIP string) int {
	t.Helper()

	_, reservation, _, err := server.passiveListeners.reserve(net.ParseIP(remoteIP), &PortRange{Start: 0, End: 0})
	require.NoError(t, err)

	addr, ok := reservation.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

// newMultiplexingTestServer listens on listener with one passive port reserved and returns that port
func newMultiplexingTestServer(t *testing.T, listener net.Listener) (*FtpServer, int) {
	t.Helper()

	server := NewFtpServer(&TestServerDriver{
		Settings: &Settings{
			Listener:                        listener,
			PassiveTransferPortRange:        &PortRange{Start: 0, End: 0},
			PassiveTransferPortMultiplexing: true,
		},
	})
	server.Logger = slog.New(slog.DiscardHandler)

	t.Cleanup(func() {
		// Best effort: the test may already have closed both
		_ = listener.Close()
		if server.passiveListeners != nil {
			_ = server.passiveListeners.close()
		}
	})

	require.NoError(t, server.Listen())

	return server, reservePassivePort(t, server, "127.0.0.1")
}

func TestServerStopClosesPassiveListenersWhenListenerCloseFails(t *testing.T) {
	req := require.New(t)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	req.NoError(err)

	server, port := newMultiplexingTestServer(t, &failingCloseListener{Listener: listener})

	err = server.Stop()
	req.ErrorIs(err, errTestListenerClose)

	var netErr NetworkError
	req.ErrorAs(err, &netErr)
	requirePortReleased(t, port)
}

func TestServerStopClosesPassiveListenersAfterListenerClosed(t *testing.T) {
	req := require.New(t)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	req.NoError(err)

	server, port := newMultiplexingTestServer(t, listener)

	serveErr := make(chan error)
	go func() { serveErr <- server.Serve() }()

	req.NoError(listener.Close())
	req.NoError(<-serveErr)

	// Closing the listener only stops new clients: the connected ones can still open passive connections
	req.Equal(port, reservePassivePort(t, server, "127.0.0.2"))

	// Serve dropped the closed listener, so Stop has no listener left to close
	req.ErrorIs(server.Stop(), ErrNotListening)
	requirePortReleased(t, port)
}

func TestServerStaleServeKeepsRestartedListener(t *testing.T) {
	req := require.New(t)

	firstListener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	req.NoError(err)

	first := &acceptNotifyListener{Listener: firstListener, accepting: make(chan struct{}), gate: make(chan struct{})}
	server, _ := newMultiplexingTestServer(t, first)

	firstServe := make(chan error, 1)
	go func() { firstServe <- server.Serve() }()
	<-first.accepting

	// The first Serve handles its closed listener only once the gate opens, after the server listens again
	req.NoError(server.Stop())

	secondListener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	req.NoError(err)
	t.Cleanup(func() { _ = secondListener.Close() })

	second := &acceptNotifyListener{Listener: secondListener, accepting: make(chan struct{})}
	server.settings.Listener = second
	req.NoError(server.Listen())

	secondServe := make(chan error, 1)
	go func() { secondServe <- server.Serve() }()
	<-second.accepting

	passivePort := reservePassivePort(t, server, "127.0.0.1")

	close(first.gate)
	req.NoError(<-firstServe)

	req.Equal(secondListener.Addr().String(), server.Addr())
	req.NoError(server.Stop())

	controlAddr, ok := secondListener.Addr().(*net.TCPAddr)
	req.True(ok)
	requirePortReleased(t, controlAddr.Port)
	requirePortReleased(t, passivePort)
	req.NoError(<-secondServe)
}

// Run it with -race: it catches unsynchronised access between Listen and Stop to the listener and the passive
// listeners manager
func TestServerStopDuringListenAndServe(t *testing.T) {
	req := require.New(t)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", testServerListenAddr)
	req.NoError(err)

	notifyListener := &acceptNotifyListener{Listener: listener, accepting: make(chan struct{})}
	server := NewFtpServer(&TestServerDriver{
		Settings: &Settings{
			Listener:                        notifyListener,
			PassiveTransferPortRange:        &PortRange{Start: 0, End: 0},
			PassiveTransferPortMultiplexing: true,
		},
	})
	server.Logger = slog.New(slog.DiscardHandler)

	t.Cleanup(func() {
		// Best effort: the test may already have stopped the server
		_ = listener.Close()
		if server.passiveListeners != nil {
			_ = server.passiveListeners.close()
		}
	})

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	stopErr := server.Stop()

	select {
	case <-notifyListener.accepting:
	case err := <-serveErr:
		// A startup failure ends here instead of blocking. Otherwise only a Stop that closed this run's listener
		// can end it this early.
		req.NoError(err)
		req.NoError(stopErr)

		return
	}

	if errors.Is(stopErr, ErrNotListening) {
		// Stop ran before this run was published, so it must not have closed the run's passive listeners
		port := reservePassivePort(t, server, "127.0.0.1")

		req.NoError(server.Stop())
		requirePortReleased(t, port)
	} else {
		req.NoError(stopErr)
	}

	req.NoError(<-serveErr)
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
