package ftpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
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
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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

func TestPassiveReservationCloseKeepsNewerReservation(t *testing.T) {
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler), nil)
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
		manager := newPassiveListenersManager(slog.New(slog.DiscardHandler), nil)
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
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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
	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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
	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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
	manager := newPassiveListenersManager(slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
	req.NoError(manager.close())
	req.NoError(manager.close())

	_, err := manager.getOrCreate(port)
	req.ErrorIs(err, net.ErrClosed)

	listener, err := newSharedPassiveListener(port, slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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

	_, err = newSharedPassiveListener(-1, slog.New(slog.NewTextHandler(io.Discard, nil)), nil) //nolint:sloglint
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

	// The shared port is wrapped once, not on each PASV
	req.Equal(int32(1), driver.passiveWrapCalls.Load())
}

// proxyHeaderTestListener mimics a PROXY protocol listener: its connections read a
// "PROXY TCP4 <src> <dst> <srcport> <dstport>\r\n" header when their remote address is first asked for
type proxyHeaderTestListener struct {
	net.Listener
}

type proxyHeaderTestConn struct {
	net.Conn
	once       sync.Once
	remoteAddr net.Addr
}

func (l *proxyHeaderTestListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &proxyHeaderTestConn{Conn: conn}, nil
}

func (c *proxyHeaderTestConn) RemoteAddr() net.Addr {
	c.once.Do(func() {
		c.remoteAddr = c.Conn.RemoteAddr()

		// One byte at a time, to leave what follows the header to the reader
		var header []byte
		buf := make([]byte, 1)

		for !bytes.HasSuffix(header, []byte("\r\n")) {
			if _, err := c.Read(buf); err != nil {
				return
			}

			header = append(header, buf[0])
		}

		if fields := strings.Fields(string(header)); len(fields) == 6 && fields[0] == "PROXY" {
			c.remoteAddr = &net.TCPAddr{IP: net.ParseIP(fields[2])}
		}
	})

	return c.remoteAddr
}

func newProxyHeaderTestManager() *passiveListenersManager {
	return newPassiveListenersManager(slog.New(slog.DiscardHandler), func(listener net.Listener) (net.Listener, error) {
		return &proxyHeaderTestListener{Listener: listener}, nil
	})
}

func dialPassivePort(t *testing.T, port int, data string) net.Conn {
	t.Helper()

	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	if data != "" {
		_, err = conn.Write([]byte(data))
		require.NoError(t, err)
	}

	return conn
}

func TestPassiveMultiplexingMatchesProxiedClientIP(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newProxyHeaderTestManager()
	defer func() { req.NoError(manager.close()) }()

	_, reservation, deadlineSetter, err := manager.reserve(net.ParseIP("192.0.2.10"), PortRange{Start: port, End: port})
	req.NoError(err)
	defer func() { _ = reservation.Close() }()

	client := dialPassivePort(t, port, fmt.Sprintf("PROXY TCP4 192.0.2.10 127.0.0.1 40000 %d\r\nhello", port))
	defer func() { _ = client.Close() }()

	req.NoError(deadlineSetter.SetDeadline(time.Now().Add(5 * time.Second)))
	conn, err := reservation.Accept()
	req.NoError(err)
	defer func() { _ = conn.Close() }()

	payload := make([]byte, 5)
	_, err = io.ReadFull(conn, payload)
	req.NoError(err)
	req.Equal("hello", string(payload))
}

func TestPassiveMultiplexingSilentClientDoesNotBlockPort(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newProxyHeaderTestManager()
	defer func() { req.NoError(manager.close()) }()

	portRange := PortRange{Start: port, End: port}

	_, first, _, err := manager.reserve(net.ParseIP("192.0.2.10"), portRange)
	req.NoError(err)
	defer func() { _ = first.Close() }()

	_, second, secondDeadline, err := manager.reserve(net.ParseIP("192.0.2.11"), portRange)
	req.NoError(err)
	defer func() { _ = second.Close() }()

	// Accepted first, this connection never sends its header
	silent := dialPassivePort(t, port, "")
	defer func() { _ = silent.Close() }()

	client := dialPassivePort(t, port, fmt.Sprintf("PROXY TCP4 192.0.2.11 127.0.0.1 40000 %d\r\n", port))
	defer func() { _ = client.Close() }()

	req.NoError(secondDeadline.SetDeadline(time.Now().Add(2 * time.Second)))
	conn, err := second.Accept()
	req.NoError(err)
	req.NoError(conn.Close())
}

func TestPassiveMultiplexingWrapErrorReleasesPort(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler), func(net.Listener) (net.Listener, error) {
		return nil, errTestPassiveFailure
	})
	defer func() { req.NoError(manager.close()) }()

	_, listener, _, err := manager.reserve(net.ParseIP("127.0.0.1"), PortRange{Start: port, End: port})
	req.ErrorIs(err, ErrNoAvailableListeningPort)
	req.Nil(listener)

	tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: port})
	req.NoError(err)
	req.NoError(tcpListener.Close())
}

func TestSharedPassiveListenerAnyIPReservationHasPortToItself(t *testing.T) {
	req := require.New(t)
	listener, err := newSharedPassiveListener(getFreePassivePort(t), slog.New(slog.DiscardHandler), nil)
	req.NoError(err)
	defer func() { req.NoError(listener.close()) }()

	byIP, err := listener.reserve(net.ParseIP("127.0.0.2"))
	req.NoError(err)

	_, err = listener.reserve(nil)
	req.ErrorIs(err, errPassiveListenerReservedForIP)

	req.NoError(byIP.Close())

	anyIP, err := listener.reserve(nil)
	req.NoError(err)

	_, err = listener.reserve(net.ParseIP("127.0.0.3"))
	req.ErrorIs(err, errPassiveListenerReservedForIP)
	_, err = listener.reserve(nil)
	req.ErrorIs(err, errPassiveListenerReservedForIP)

	req.NoError(anyIP.Close())

	byIP, err = listener.reserve(net.ParseIP("127.0.0.3"))
	req.NoError(err)
	req.NoError(byIP.Close())
}

func TestPassiveListenersManagerAnyIPReservation(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler), nil)
	defer func() { req.NoError(manager.close()) }()

	portRange := PortRange{Start: port, End: port}

	// The connection comes from 127.0.0.1, which is not the IP of any reservation
	_, reservation, deadlineSetter, err := manager.reserve(nil, portRange)
	req.NoError(err)
	defer func() { _ = reservation.Close() }()

	client := dialPassivePort(t, port, "")
	defer func() { _ = client.Close() }()

	req.NoError(deadlineSetter.SetDeadline(time.Now().Add(5 * time.Second)))
	conn, err := reservation.Accept()
	req.NoError(err)
	req.NoError(conn.Close())

	// Once used, the reservation no longer holds the port
	_, next, _, err := manager.reserve(net.ParseIP("192.0.2.10"), portRange)
	req.NoError(err)
	req.NoError(next.Close())
}

func TestPassivePortMultiplexingIPMatchDisabled(t *testing.T) {
	// 127.0.1.1 is a loopback address on Linux, macOS needs 'sudo ifconfig lo0 alias 127.0.1.1 up'
	probe, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.1.1")})
	if err != nil {
		t.Skip("Skipping test: 127.0.1.1 not available")
	}

	req := require.New(t)
	req.NoError(probe.Close())

	port := getFreePassivePort(t)
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Settings: &Settings{
			ListenAddr:                      testServerListenAddr,
			PassiveTransferPortRange:        &PortRange{Start: port, End: port},
			PassiveTransferPortMultiplexing: true,
			PasvConnectionsCheck:            IPMatchDisabled,
		},
	})

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
	req.NoError(err)
	defer func() { req.NoError(conn.Close()) }()

	buf := make([]byte, 1024)
	_, err = conn.Read(buf)
	req.NoError(err)

	loginConnection(t, conn)

	_, err = conn.Write([]byte("PASV\r\n"))
	req.NoError(err)

	readBytes, err := conn.Read(buf)
	req.NoError(err)
	req.Equal(port, getPortFromPASVResponse(t, string(buf[:readBytes])))

	_, err = conn.Write([]byte("LIST\r\n"))
	req.NoError(err)

	// The control connection comes from 127.0.0.1, the data connection from 127.0.1.1
	dataDialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.1.1")}, Timeout: 5 * time.Second}
	dataConn, err := dataDialer.DialContext(t.Context(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	req.NoError(err)
	defer func() { _ = dataConn.Close() }()

	readBytes, err = conn.Read(buf)
	req.NoError(err)
	req.True(strings.HasPrefix(string(buf[:readBytes]), "150 Using transfer connection"), string(buf[:readBytes]))
}

var errTestRefusedPeer = errors.New("refused peer")

// failOnceTestListener fails the Accept of the first connection it gets, like a wrapper refusing a peer
type failOnceTestListener struct {
	net.Listener
	failed atomic.Bool
}

func (l *failOnceTestListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	if l.failed.CompareAndSwap(false, true) {
		_ = conn.Close()

		return nil, errTestRefusedPeer
	}

	return conn, nil
}

func newFailOnceTestManager() (*passiveListenersManager, *failOnceTestListener) {
	wrapped := &failOnceTestListener{}

	return newPassiveListenersManager(slog.New(slog.DiscardHandler), func(listener net.Listener) (net.Listener, error) {
		wrapped.Listener = listener

		return wrapped, nil
	}), wrapped
}

func TestPassiveMultiplexingWrapperAcceptErrorKeepsPort(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager, wrapped := newFailOnceTestManager()
	defer func() { req.NoError(manager.close()) }()

	portRange := PortRange{Start: port, End: port}
	clientIP := net.ParseIP("127.0.0.1")

	_, reservation, _, err := manager.reserve(clientIP, portRange)
	req.NoError(err)
	req.NoError(reservation.Close())

	// A connection the wrapper refuses
	stray := dialPassivePort(t, port, "")
	req.NoError(stray.Close())
	req.Eventually(wrapped.failed.Load, 2*time.Second, 10*time.Millisecond)

	_, reservation, deadlineSetter, err := manager.reserve(clientIP, portRange)
	req.NoError(err)
	defer func() { _ = reservation.Close() }()

	client := dialPassivePort(t, port, "hello")
	defer func() { _ = client.Close() }()

	req.NoError(deadlineSetter.SetDeadline(time.Now().Add(5 * time.Second)))
	conn, err := reservation.Accept()
	req.NoError(err)
	defer func() { _ = conn.Close() }()

	payload := make([]byte, 5)
	_, err = io.ReadFull(conn, payload)
	req.NoError(err)
	req.Equal("hello", string(payload))
}

// closedErrorTestListener reports itself closed on every Accept without closing the TCP listener
type closedErrorTestListener struct {
	net.Listener
	closed chan struct{}
}

func (l *closedErrorTestListener) Accept() (net.Conn, error) {
	<-l.closed

	return nil, fmt.Errorf("wrapper closed: %w", net.ErrClosed)
}

func TestPassiveMultiplexingWrapperClosedErrorStopsPort(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	wrapped := &closedErrorTestListener{closed: make(chan struct{})}
	wrap := func(listener net.Listener) (net.Listener, error) {
		wrapped.Listener = listener

		return wrapped, nil
	}
	manager := newPassiveListenersManager(slog.New(slog.DiscardHandler), wrap)
	defer func() { req.NoError(manager.close()) }()

	_, reservation, deadlineSetter, err := manager.reserve(net.ParseIP("127.0.0.1"), PortRange{Start: port, End: port})
	req.NoError(err)

	close(wrapped.closed)

	// The waiting reservation is failed instead of hanging
	req.NoError(deadlineSetter.SetDeadline(time.Now().Add(5 * time.Second)))
	_, err = reservation.Accept()
	req.ErrorIs(err, net.ErrClosed)
}

func TestPassiveMultiplexingCloseFailsWaitingReservation(t *testing.T) {
	req := require.New(t)
	port := getFreePassivePort(t)
	manager, _ := newFailOnceTestManager()

	_, reservation, deadlineSetter, err := manager.reserve(net.ParseIP("127.0.0.1"), PortRange{Start: port, End: port})
	req.NoError(err)
	req.NoError(deadlineSetter.SetDeadline(time.Now().Add(5 * time.Second)))

	accepted := make(chan error, 1)
	go func() {
		_, acceptErr := reservation.Accept()
		accepted <- acceptErr
	}()

	req.NoError(manager.close())

	select {
	case err = <-accepted:
		req.Error(err)
		req.NotErrorIs(err, os.ErrDeadlineExceeded)
	case <-time.After(3 * time.Second):
		req.Fail("Accept did not return after the shared listener was closed")
	}
}
