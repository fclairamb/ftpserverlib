package ftpserver

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPortRangeFetchNextFailure tests when FetchNext returns false
func TestPortRangeFetchNextFailure(t *testing.T) {
	req := require.New(t)

	// Create a mock port mapping that always returns false
	mockPortMapping := &mockFailingPortMapping{}

	// Create a test server
	server := NewTestServer(t, false)
	drv, ok := server.driver.(*TestServerDriver)
	if !ok {
		t.Fatalf("server.driver is not *TestServerDriver")
	}
	driver := NewTestClientDriver(drv)

	// Create a client handler
	handler := &clientHandler{
		server: server,
		driver: driver,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), //nolint:sloglint // DiscardHandler requires Go 1.23+
	}

	// Set the mock port mapping
	server.settings.PassiveTransferPortRange = mockPortMapping

	// Try to get a passive port - this should fail immediately
	exposedPort, listener, err := handler.getPassivePort()

	// Should return an error indicating no available ports
	req.Error(err)
	req.Equal(ErrNoAvailableListeningPort, err)
	req.Equal(0, exposedPort)
	req.Nil(listener)
}

// mockFailingPortMapping is a mock that always fails to provide ports
type mockFailingPortMapping struct{}

func (m *mockFailingPortMapping) FetchNext() (int, int, bool) {
	return 0, 0, false // Always fail
}

func (m *mockFailingPortMapping) NumberAttempts() int {
	return 1 // Minimal attempts
}

// getPassivePort is a test helper to call findListenerWithinPortRange with the current PassiveTransferPortRange
func (h *clientHandler) getPassivePort() (int, *net.TCPListener, error) {
	return h.findListenerWithinPortRange(h.server.settings.PassiveTransferPortRange)
}
