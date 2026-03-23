package ftpserver

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

type trackingTestConn struct {
	testNetConn
	closed bool
}

func (c *trackingTestConn) Close() error {
	c.closed = true

	return nil
}

func getFreePassivePort(t *testing.T) int {
	t.Helper()

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, listener.Close())
	}()

	addr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
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
	clientIP := net.ParseIP("127.0.0.2")

	exposedPort, listener, deadlineSetter, err := manager.reserve(clientIP, portRange)
	req.Equal(port, exposedPort)
	req.NoError(err)
	req.NotNil(listener)
	req.NotNil(deadlineSetter)

	exposedPort, listener, deadlineSetter, err = manager.reserve(clientIP, portRange)
	req.ErrorIs(err, ErrNoAvailableListeningPort)
	req.Zero(exposedPort)
	req.Nil(listener)
	req.Nil(deadlineSetter)
}

func TestPassiveListenersManagerCloseReleasesReservation(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	defer func() {
		req.NoError(manager.close())
	}()

	portRange := &PortRange{Start: port, End: port}
	clientIP := net.ParseIP("127.0.0.2")

	exposedPort, listener, deadlineSetter, err := manager.reserve(clientIP, portRange)
	req.NoError(err)
	req.Equal(port, exposedPort)
	req.NotNil(deadlineSetter)
	req.NoError(listener.Close())

	exposedPort, listener, deadlineSetter, err = manager.reserve(clientIP, portRange)
	req.NoError(err)
	req.Equal(port, exposedPort)
	req.NotNil(listener)
	req.NotNil(deadlineSetter)
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

func TestPassiveReservationListenerTimeoutAndHelpers(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	req.NoError(err)
	defer func() {
		req.NoError(listener.close())
	}()

	reservation, err := listener.reserve(net.ParseIP("127.0.0.2"))
	req.NoError(err)
	req.Equal(listener.listener.Addr(), reservation.Addr())

	req.NoError(reservation.SetDeadline(time.Now().Add(-time.Second)))

	_, err = reservation.Accept()
	req.Error(err)

	var opErr *net.OpError
	req.ErrorAs(err, &opErr)

	netErr, ok := opErr.Err.(net.Error)
	req.True(ok)
	req.Equal("i/o timeout", netErr.Error())
	req.True(netErr.Timeout())
	req.True(netErr.Temporary())

	req.True(isClosedListenerError(net.ErrClosed))
	req.True(isClosedListenerError(&net.OpError{Err: errors.New("use of closed network connection")}))
	req.False(isClosedListenerError(errors.New("different error")))
}

func TestPassiveReservationListenerCloseAndFailures(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	req.NoError(err)
	defer func() {
		req.NoError(listener.close())
	}()

	reservation, err := listener.reserve(net.ParseIP("127.0.0.2"))
	req.NoError(err)

	expectedErr := errors.New("boom")
	reservation.stateMu.Lock()
	reservation.failureErr = expectedErr
	reservation.stateMu.Unlock()
	reservation.connCh <- nil

	_, err = reservation.Accept()
	req.ErrorIs(err, expectedErr)

	reservation2, err := listener.reserve(net.ParseIP("127.0.0.3"))
	req.NoError(err)

	conn := &trackingTestConn{}
	reservation2.connCh <- conn
	req.NoError(reservation2.Close())
	req.True(conn.closed)
	req.False(reservation2.deliver(&trackingTestConn{}))

	_, err = reservation2.Accept()
	req.ErrorIs(err, net.ErrClosed)
}

func TestSharedPassiveListenerDispatchRejectionsAndClosedManager(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	req.NoError(manager.close())
	req.NoError(manager.close())

	_, err := manager.getOrCreate(port)
	req.ErrorIs(err, net.ErrClosed)

	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	req.NoError(err)

	unknownConn := &trackingTestConn{
		testNetConn: testNetConn{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.9"), Port: 40003}},
	}
	listener.dispatch(unknownConn)
	req.True(unknownConn.closed)

	invalidConn := &trackingTestConn{
		testNetConn: testNetConn{remoteAddr: &net.UnixAddr{Name: "sock", Net: "unix"}},
	}
	listener.dispatch(invalidConn)
	req.True(invalidConn.closed)

	req.NoError(listener.close())
	_, err = listener.reserve(net.ParseIP("127.0.0.4"))
	req.ErrorIs(err, net.ErrClosed)

	_, err = newSharedPassiveListener(-1, slog.New(slog.NewTextHandler(io.Discard, nil))) //nolint:sloglint
	req.Error(err)
}
