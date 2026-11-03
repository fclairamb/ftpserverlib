package ftpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

type trackingTestConn struct {
	testNetConn
	closed bool
}

type closeCountingConn struct {
	testNetConn
	closed atomic.Bool
}

const testServerListenAddr = "127.0.0.1:0"

var (
	errTestClosedListener = errors.New("use of closed network connection")
	errTestDifferent      = errors.New("different error")
	errTestPassiveFailure = errors.New("boom")
)

func (c *trackingTestConn) Close() error {
	c.closed = true

	return nil
}

func (c *closeCountingConn) Close() error {
	c.closed.Store(true)

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

// orderedPortGetter offers its ports in a fixed order, in place of the random order of a range
type orderedPortGetter struct {
	ports []int
	next  int
}

const orderedPortGetterExposedOffset = 1000

func (g *orderedPortGetter) FetchNext() (int, int, bool) {
	if g.next == len(g.ports) {
		return 0, 0, false
	}

	port := g.ports[g.next]
	g.next++

	return port + orderedPortGetterExposedOffset, port, true
}

func (g *orderedPortGetter) NumberAttempts() int {
	return len(g.ports)
}

func TestPassiveListenersManagerPrefersBoundPorts(t *testing.T) {
	req := require.New(t)
	port1 := getFreePassivePort(t)
	port2 := getFreePassivePort(t)
	req.NotEqual(port1, port2)

	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler))
	defer func() {
		req.NoError(manager.close())
	}()

	ip1 := net.ParseIP("127.0.0.2")
	ip2 := net.ParseIP("127.0.0.3")

	exposedPort, first, _, err := manager.reserve(ip1, &orderedPortGetter{ports: []int{port1, port2}})
	req.NoError(err)
	req.Equal(port1+orderedPortGetterExposedOffset, exposedPort)

	// port2 comes first, but port1 is bound and free for ip2
	exposedPort, _, _, err = manager.reserve(ip2, &orderedPortGetter{ports: []int{port2, port1}})
	req.NoError(err)
	req.Equal(port1+orderedPortGetterExposedOffset, exposedPort)
	req.Len(manager.listeners, 1)

	// port1 is reserved for ip1, so a second reservation for it binds port2
	exposedPort, _, _, err = manager.reserve(ip1, &orderedPortGetter{ports: []int{port1, port2}})
	req.NoError(err)
	req.Equal(port2+orderedPortGetterExposedOffset, exposedPort)
	req.Len(manager.listeners, 2)

	req.NoError(first.Close())

	exposedPort, _, _, err = manager.reserve(ip1, &orderedPortGetter{ports: []int{port2, port1}})
	req.NoError(err)
	req.Equal(port1+orderedPortGetterExposedOffset, exposedPort)
}

func TestPassiveReservationCloseKeepsNewerReservation(t *testing.T) {
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler))
	defer func() {
		require.NoError(t, manager.close())
	}()

	portRange := PortRange{Start: port, End: port}
	clientIP := net.ParseIP("127.0.0.1")

	_, first, _, err := manager.reserve(clientIP, portRange)
	require.NoError(t, err)

	dialer := net.Dialer{Timeout: time.Second}
	firstClient, err := dialer.DialContext(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer func() { _ = firstClient.Close() }()

	firstConn, err := first.Accept()
	require.NoError(t, err)
	defer func() { _ = firstConn.Close() }()

	// The first data connection consumed its reservation, so the port is free for the same IP
	_, second, secondDeadline, err := manager.reserve(clientIP, portRange)
	require.NoError(t, err)
	defer func() { _ = second.Close() }()

	require.NoError(t, first.Close())

	secondClient, err := dialer.DialContext(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer func() { _ = secondClient.Close() }()

	require.NoError(t, secondDeadline.SetDeadline(time.Now().Add(5*time.Second)))
	secondConn, err := second.Accept()
	require.NoError(t, err)
	require.NoError(t, secondConn.Close())
}

func TestPassiveReservationDeliverCloseRace(t *testing.T) {
	const rounds = 50000

	parent := &sharedPassiveListener{
		logger:       slog.New(slog.DiscardHandler),
		reservations: make(map[string]*passiveReservationListener),
	}
	clientIP := net.ParseIP("127.0.0.1")

	for range rounds {
		reservation, err := parent.reserve(clientIP)
		require.NoError(t, err)
		delete(parent.reservations, clientIP.String()) // as dispatch does before calling deliver

		conn := &closeCountingConn{}
		start := make(chan struct{})
		var delivered bool
		var waitGroup sync.WaitGroup

		waitGroup.Go(func() {
			<-start
			delivered = reservation.deliver(conn)
		})
		waitGroup.Go(func() {
			<-start
			_ = reservation.Close()
		})
		close(start)
		waitGroup.Wait()

		if delivered {
			require.True(t, conn.closed.Load(), "the connection was left with nobody to read or close it")
		}
		require.Empty(t, reservation.connCh)
	}
}

func TestPassiveListenersManagerConcurrentFirstUse(t *testing.T) {
	const (
		rounds  = 50
		clients = 8
	)

	for range rounds {
		port := getFreePassivePort(t)
		manager := newPassiveListenersManager(slog.New(slog.DiscardHandler))
		portRange := PortRange{Start: port, End: port}
		errs := make([]error, clients)

		var waitGroup sync.WaitGroup
		for client := range clients {
			waitGroup.Go(func() {
				_, _, _, errs[client] = manager.reserve(net.ParseIP(fmt.Sprintf("127.0.0.%d", client+1)), portRange)
			})
		}
		waitGroup.Wait()

		for _, err := range errs {
			require.NoError(t, err)
		}
		require.NoError(t, manager.close())
	}
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
			ListenAddr:                      testServerListenAddr,
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

	var timeoutErr passiveAcceptTimeoutError
	req.ErrorAs(err, &timeoutErr)
	req.Equal("i/o timeout", timeoutErr.Error())
	req.True(timeoutErr.Timeout())

	req.True(isClosedListenerError(net.ErrClosed))
	req.True(isClosedListenerError(&net.OpError{Err: errTestClosedListener}))
	req.False(isClosedListenerError(errTestDifferent))
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

	expectedErr := errTestPassiveFailure
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

	_, err := manager.getOrCreate(passivePortCandidate{exposedPort: port, listenedPort: port})
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

func TestPassivePortMultiplexingPORTReleasesReservation(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	driver := &TestServerDriver{
		Settings: &Settings{
			ListenAddr:                      testServerListenAddr,
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

	expected := fmt.Sprintf(",%d,%d)", port/256, port%256)

	for range 3 {
		returnCode, message, errCmd := raw.SendCommand("PASV")
		req.NoError(errCmd)
		req.Equal(StatusEnteringPASV, returnCode, message)
		req.Contains(message, expected)

		returnCode, message, errCmd = raw.SendCommand("PORT 127,0,0,1,%d,%d", port/256, port%256)
		req.NoError(errCmd)
		req.Equal(StatusOK, returnCode, message)
	}
}
