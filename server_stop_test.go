package ftpserver

import (
	"sync"
	"testing"
	"time"

	log "github.com/fclairamb/go-log"
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
	mockLogger := &MockLogger{}
	server.Logger = mockLogger

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
	req.Empty(mockLogger.ErrorLogs, "Expected no error logs when stopping server, but got: %v", mockLogger.ErrorLogs)
}

// MockLogger captures log calls to verify behavior
type MockLogger struct {
	ErrorLogs []string
	WarnLogs  []string
	InfoLogs  []string
	DebugLogs []string
}

func (m *MockLogger) Debug(message string, _ ...interface{}) {
	m.DebugLogs = append(m.DebugLogs, message)
}

func (m *MockLogger) Info(message string, _ ...interface{}) {
	m.InfoLogs = append(m.InfoLogs, message)
}

func (m *MockLogger) Warn(message string, _ ...interface{}) {
	m.WarnLogs = append(m.WarnLogs, message)
}

func (m *MockLogger) Error(message string, _ ...interface{}) {
	m.ErrorLogs = append(m.ErrorLogs, message)
}

func (m *MockLogger) Panic(message string, _ ...interface{}) {
	panic(message)
}

func (m *MockLogger) With(_ ...interface{}) log.Logger {
	return m
}
