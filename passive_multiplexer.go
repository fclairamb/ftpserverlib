package ftpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

const errStrClosedNetworkConnection = "use of closed network connection"

var errPassiveListenerReservedForIP = errors.New("passive listener already reserved for client ip")

// anyIPKey is the reservation key of a client whose data connection may come from any IP. Such a
// reservation can't be told apart from others by IP, so it has the port to itself until it is used.
const anyIPKey = ""

type passiveDeadlineSetter interface {
	SetDeadline(deadline time.Time) error
}

type passivePortCandidate struct {
	exposedPort  int
	listenedPort int
}

// passiveListenerWrapper wraps the listener of a shared port, see MainDriverExtensionPassiveWrapper
type passiveListenerWrapper func(listener net.Listener) (net.Listener, error)

type passiveListenersManager struct {
	logger    *slog.Logger
	wrap      passiveListenerWrapper
	mu        sync.Mutex
	listeners map[int]*sharedPassiveListener
	closed    bool
}

func newPassiveListenersManager(logger *slog.Logger, wrap passiveListenerWrapper) *passiveListenersManager {
	return &passiveListenersManager{
		logger:    logger,
		wrap:      wrap,
		listeners: make(map[int]*sharedPassiveListener),
	}
}

func (m *passiveListenersManager) reserve(
	remoteIP net.IP,
	portRange PasvPortGetter,
) (int, net.Listener, passiveDeadlineSetter, error) {
	for _, candidate := range getPassivePortCandidates(portRange) {
		listener, err := m.getOrCreate(candidate.listenedPort)
		if err != nil {
			continue
		}

		reservation, err := listener.reserve(remoteIP)
		if err == nil {
			return candidate.exposedPort, reservation, reservation, nil
		}
		if errors.Is(err, errPassiveListenerReservedForIP) {
			continue
		}
	}

	return 0, nil, nil, ErrNoAvailableListeningPort
}

func (m *passiveListenersManager) close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()

		return nil
	}

	m.closed = true
	listeners := make([]*sharedPassiveListener, 0, len(m.listeners))
	for _, listener := range m.listeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()

	var closeErr error
	for _, listener := range listeners {
		if err := listener.close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	return closeErr
}

func (m *passiveListenersManager) getOrCreate(port int) (*sharedPassiveListener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, net.ErrClosed
	}
	if listener, ok := m.listeners[port]; ok {
		return listener, nil
	}

	listener, err := newSharedPassiveListener(port, m.logger.With("passivePort", port), m.wrap)
	if err != nil {
		return nil, err
	}

	m.listeners[port] = listener

	return listener, nil
}

type sharedPassiveListener struct {
	logger       *slog.Logger
	listener     net.Listener
	mu           sync.Mutex
	reservations map[string]*passiveReservationListener
	closed       bool
}

func newSharedPassiveListener(
	port int,
	logger *slog.Logger,
	wrap passiveListenerWrapper,
) (*sharedPassiveListener, error) {
	laddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, newNetworkError(fmt.Sprintf("could not resolve port %d", port), err)
	}

	tcpListener, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return nil, err
	}

	// Wrapping happens before connections are matched by IP, so a wrapper reading the PROXY protocol
	// header gets them matched on the client's IP instead of the proxy's
	var listener net.Listener = tcpListener
	if wrap != nil {
		if listener, err = wrap(tcpListener); err != nil {
			_ = tcpListener.Close()

			return nil, err
		}
	}

	result := &sharedPassiveListener{
		logger:       logger,
		listener:     listener,
		reservations: make(map[string]*passiveReservationListener),
	}

	go result.serve()

	return result, nil
}

func (l *sharedPassiveListener) serve() {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if isClosedListenerError(err) {
				l.failAll(net.ErrClosed)

				return
			}

			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() { //nolint:staticcheck
				l.logger.Warn("Temporary passive accept error", "err", err)

				continue
			}

			l.failAll(err)

			return
		}

		// A wrapper may read from the connection to get its remote address, which must not block the
		// other clients of this port
		go l.dispatch(conn)
	}
}

// reserve reserves the port for the next data connection from remoteIP, or from any IP if it is nil
func (l *sharedPassiveListener) reserve(remoteIP net.IP) (*passiveReservationListener, error) {
	key := anyIPKey
	if remoteIP != nil {
		key = remoteIP.String()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil, net.ErrClosed
	}

	_, taken := l.reservations[key]
	_, takenByAnyIP := l.reservations[anyIPKey]

	if taken || takenByAnyIP || (key == anyIPKey && len(l.reservations) > 0) {
		return nil, errPassiveListenerReservedForIP
	}

	reservation := &passiveReservationListener{
		parent:   l,
		remoteIP: key,
		connCh:   make(chan net.Conn, 1),
		closedCh: make(chan struct{}),
	}
	l.reservations[key] = reservation

	return reservation, nil
}

func (l *sharedPassiveListener) dispatch(conn net.Conn) {
	ipAddress, err := getIPFromRemoteAddr(conn.RemoteAddr())
	if err != nil {
		l.logger.Warn("Could not parse passive data connection IP", "err", err)
		_ = conn.Close()

		return
	}

	key := ipAddress.String()

	l.mu.Lock()
	reservation, ok := l.reservations[key]
	if !ok {
		key = anyIPKey
		reservation = l.reservations[key]
	}
	if reservation != nil {
		delete(l.reservations, key)
	}
	l.mu.Unlock()

	if reservation == nil || !reservation.deliver(conn) {
		_ = conn.Close()
	}
}

func (l *sharedPassiveListener) release(reservation *passiveReservationListener) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.reservations[reservation.remoteIP] == reservation {
		delete(l.reservations, reservation.remoteIP)
	}
}

func (l *sharedPassiveListener) failAll(err error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()

		return
	}

	l.closed = true
	reservations := make([]*passiveReservationListener, 0, len(l.reservations))
	for _, reservation := range l.reservations {
		reservations = append(reservations, reservation)
	}
	l.reservations = nil
	l.mu.Unlock()

	for _, reservation := range reservations {
		reservation.fail(err)
	}
}

func (l *sharedPassiveListener) close() error {
	err := l.listener.Close()
	l.failAll(net.ErrClosed)

	return err
}

type passiveReservationListener struct {
	parent     *sharedPassiveListener
	remoteIP   string
	connCh     chan net.Conn
	closedCh   chan struct{}
	closeOnce  sync.Once
	stateMu    sync.Mutex
	deadline   time.Time
	closing    bool
	failureErr error
}

func (l *passiveReservationListener) Accept() (net.Conn, error) {
	timeout := l.getDeadline()
	var timerCh <-chan time.Time
	var timer *time.Timer

	if !timeout.IsZero() {
		wait := time.Until(timeout)
		if wait <= 0 {
			return nil, newPassiveAcceptTimeoutError()
		}

		timer = time.NewTimer(wait)
		timerCh = timer.C
		defer timer.Stop()
	}

	select {
	case conn := <-l.connCh:
		if conn == nil {
			return nil, l.getFailure()
		}

		return conn, nil
	case <-l.closedCh:
		return nil, l.getFailure()
	case <-timerCh:
		return nil, newPassiveAcceptTimeoutError()
	}
}

func (l *passiveReservationListener) Close() error {
	l.closeOnce.Do(func() {
		l.parent.release(l)
		l.markClosing()

		select {
		case conn := <-l.connCh:
			if conn != nil {
				_ = conn.Close()
			}
		default:
		}

		close(l.closedCh)
	})

	return nil
}

func (l *passiveReservationListener) Addr() net.Addr {
	return l.parent.listener.Addr()
}

func (l *passiveReservationListener) SetDeadline(deadline time.Time) error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	l.deadline = deadline

	return nil
}

// deliver hands conn over unless Close started: connCh has room for the single connection a reservation
// gets, so the send cannot block, and Close drains connCh only after setting closing.
func (l *passiveReservationListener) deliver(conn net.Conn) bool {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	if l.closing {
		return false
	}

	l.connCh <- conn

	return true
}

func (l *passiveReservationListener) markClosing() {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	l.closing = true
}

func (l *passiveReservationListener) fail(err error) {
	l.stateMu.Lock()
	if l.failureErr == nil {
		l.failureErr = err
	}
	l.stateMu.Unlock()

	_ = l.Close()
}

func (l *passiveReservationListener) getDeadline() time.Time {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	return l.deadline
}

func (l *passiveReservationListener) getFailure() error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	if l.failureErr != nil {
		return l.failureErr
	}

	return net.ErrClosed
}

type passiveAcceptTimeoutError struct{}

func (passiveAcceptTimeoutError) Error() string   { return "i/o timeout" }
func (passiveAcceptTimeoutError) Timeout() bool   { return true }
func (passiveAcceptTimeoutError) Temporary() bool { return true }

func newPassiveAcceptTimeoutError() error {
	return &net.OpError{
		Op:  "accept",
		Net: "tcp",
		Err: passiveAcceptTimeoutError{},
	}
}

func isClosedListenerError(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	}

	errOp := &net.OpError{}
	if errors.As(err, &errOp) && errOp.Err != nil {
		return errOp.Err.Error() == errStrClosedNetworkConnection
	}

	return false
}
