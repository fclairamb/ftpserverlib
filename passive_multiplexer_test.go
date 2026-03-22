package ftpserver

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func getFreePassivePort(t *testing.T) int {
	t.Helper()

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, listener.Close())
	}()

	return listener.Addr().(*net.TCPAddr).Port
}

func TestPassiveListenersManagerMultiplexesByClientIP(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	defer func() {
		req.NoError(manager.close())
	}()

	portRange := &PortRange{Start: port, End: port}
	ip1 := net.ParseIP("127.0.0.2")
	ip2 := net.ParseIP("127.0.0.3")

	exposedPort1, listener1, _, err := manager.reserve(ip1, portRange)
	req.NoError(err)
	req.Equal(port, exposedPort1)

	exposedPort2, listener2, _, err := manager.reserve(ip2, portRange)
	req.NoError(err)
	req.Equal(port, exposedPort2)

	req.Len(manager.listeners, 1)
	sharedListener := manager.listeners[port]

	conn1 := &testNetConn{remoteAddr: &net.TCPAddr{IP: ip1, Port: 40001}}
	sharedListener.dispatch(conn1)
	accepted1, err := listener1.Accept()
	req.NoError(err)
	req.Same(conn1, accepted1)

	conn2 := &testNetConn{remoteAddr: &net.TCPAddr{IP: ip2, Port: 40002}}
	sharedListener.dispatch(conn2)
	accepted2, err := listener2.Accept()
	req.NoError(err)
	req.Same(conn2, accepted2)
}

func TestPassiveListenersManagerRejectsSameIPForSamePort(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	defer func() {
		req.NoError(manager.close())
	}()

	portRange := &PortRange{Start: port, End: port}
	ip := net.ParseIP("127.0.0.2")

	exposedPort, _, _, err := manager.reserve(ip, portRange)
	req.Equal(port, exposedPort)
	req.NoError(err)

	_, _, _, err = manager.reserve(ip, portRange)
	req.ErrorIs(err, ErrNoAvailableListeningPort)
}

func TestPassiveListenersManagerCloseReleasesReservation(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	defer func() {
		req.NoError(manager.close())
	}()

	portRange := &PortRange{Start: port, End: port}
	ip := net.ParseIP("127.0.0.2")

	_, listener, _, err := manager.reserve(ip, portRange)
	req.NoError(err)
	req.NoError(listener.Close())

	_, _, _, err = manager.reserve(ip, portRange)
	req.NoError(err)
}

func TestPassivePortMultiplexingSameClientExhaustion(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	driver := &TestServerDriver{
		Settings: &Settings{
			ListenAddr:                      "127.0.0.1:0",
			DefaultTransferType:             TransferTypeBinary,
			PassiveTransferPortRange:        &PortRange{Start: port, End: port},
			PassiveTransferPortMultiplexing: true,
		},
	}
	server := NewTestServerWithTestDriver(t, driver)

	client, err := goftp.DialConfig(goftp.Config{
		User:     authUser,
		Password: authPass,
	}, server.Addr())
	req.NoError(err)
	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	req.NoError(err)
	defer func() { req.NoError(raw.Close()) }()

	returnCode, message, err := raw.SendCommand("PASV")
	req.NoError(err)
	req.Equal(StatusEnteringPASV, returnCode, message)

	returnCode, message, err = raw.SendCommand("PASV")
	req.NoError(err)
	req.Equal(StatusServiceNotAvailable, returnCode, message)
	req.Contains(message, ErrNoAvailableListeningPort.Error())
}
