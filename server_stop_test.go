package ftpserver

import (
	"context"
	"log/slog"
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
