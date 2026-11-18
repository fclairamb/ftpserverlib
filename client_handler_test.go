package ftpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrency(t *testing.T) {
	server := NewTestServer(t, false)

	nbClients := 100

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(nbClients)

	for range nbClients {
		go func() {
			conf := goftp.Config{
				User:     authUser,
				Password: authPass,
			}

			client, err := goftp.DialConfig(conf, server.Addr())
			if err != nil {
				panic(fmt.Sprintf("Couldn't connect: %v", err))
			}

			if _, err = client.ReadDir("/"); err != nil {
				panic(fmt.Sprintf("Couldn't list dir: %v", err))
			}

			defer func() { panicOnError(client.Close()) }()

			waitGroup.Done()
		}()
	}

	waitGroup.Wait()
}

func TestDOS(t *testing.T) {
	server := NewTestServer(t, true)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 128)
	readBytes, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:readBytes])
	require.Equal(t, "220 TEST Server\r\n", response)

	written := 0

	for {
		readBytes, err = conn.Write([]byte("some text without line ending"))
		written += readBytes

		if err != nil {
			break
		}

		if written > 4096 {
			server.Logger.Warn("test DOS",
				"bytes written", written)
		}
	}
}

func TestLastCommand(t *testing.T) {
	cc := clientHandler{}
	assert.Empty(t, cc.GetLastCommand())
}

func TestLastDataChannel(t *testing.T) {
	cc := clientHandler{lastDataChannel: DataChannelPassive}
	assert.Equal(t, DataChannelPassive, cc.GetLastDataChannel())
}

func TestTransferOpenError(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// we send STOR without opening a transfer connection
	rc, response, err := raw.SendCommand("STOR file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, "unable to open transfer: no transfer connection", response)
}

func TestTLSMethods(t *testing.T) {
	t.Parallel()

	t.Run("without-tls", func(t *testing.T) {
		t.Parallel()
		cc := clientHandler{
			server: NewTestServer(t, false),
		}
		require.False(t, cc.HasTLSForControl())
		require.False(t, cc.HasTLSForTransfers())
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		t.Parallel()
		server := NewTestServerWithTestDriver(t, &TestServerDriver{
			Settings: &Settings{
				TLSRequired: ImplicitEncryption,
			},
			TLS:   true,
			Debug: false,
		})
		cc := clientHandler{
			server: server,
		}
		require.True(t, cc.HasTLSForControl())
		require.True(t, cc.HasTLSForTransfers())
	})
}

func TestConnectionNotAllowed(t *testing.T) {
	driver := &TestServerDriver{
		Debug:          true,
		CloseOnConnect: true,
	}
	s := NewTestServerWithTestDriver(t, driver)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", s.Addr())
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:n])
	require.Equal(t, "500 TEST Server\r\n", response)

	_, err = conn.Write([]byte("NOOP\r\n"))
	require.NoError(t, err)

	_, err = conn.Read(buf)
	require.Error(t, err)
}

func TestCloseConnection(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	ftpUpload(t, client, createTemporaryFile(t, 1024*1024), "file.bin")

	require.Len(t, driver.GetClientsInfo(), 1)

	err = client.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	require.Len(t, driver.GetClientsInfo(), 2)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 1
	}, 1*time.Second, 50*time.Millisecond)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 0
	}, 1*time.Second, 50*time.Millisecond)
}

// controlConn is a control connection a test opened, with its server side.
type controlConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	context ClientContext
}

// dialControlConns opens nbConns control connections and reads each one's
// welcome, so ClientConnected registered them.
func dialControlConns(t *testing.T, server *FtpServer, driver *TestServerDriver, nbConns int) []controlConn {
	t.Helper()

	conns := make([]controlConn, 0, nbConns)

	for range nbConns {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
		require.NoError(t, err)

		t.Cleanup(func() { _ = conn.Close() })

		reader := bufio.NewReader(conn)
		welcome, err := reader.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "220 TEST Server\r\n", welcome)

		clientContext := driver.clientOf(conn)
		require.NotNil(t, clientContext)

		conns = append(conns, controlConn{conn: conn, reader: reader, context: clientContext})
	}

	return conns
}

// clientOf returns the ClientContext of the control connection conn opened.
func (driver *TestServerDriver) clientOf(conn net.Conn) ClientContext {
	driver.clientMU.Lock()
	defer driver.clientMU.Unlock()

	for _, clientContext := range driver.Clients {
		if clientContext.RemoteAddr().String() == conn.LocalAddr().String() {
			return clientContext
		}
	}

	return nil
}

// TestCloseWhileClientQuits closes clients from another goroutine, as a driver
// kicking them does, while their QUIT disconnects them from their own
// goroutine. Both run disconnect: run it with -race.
func TestCloseWhileClientQuits(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)

	var waitGroup sync.WaitGroup

	for _, control := range dialControlConns(t, server, driver, 50) {
		waitGroup.Go(func() {
			_, err := control.conn.Write([]byte("QUIT\r\n"))
			assert.NoError(t, err)
		})
		waitGroup.Go(func() {
			assert.NoError(t, control.context.Close())
		})
	}

	waitGroup.Wait()
}

// TestClientContextWhileClientStartsTLS reads clients' addresses and closes
// them from another goroutine once their AUTH TLS was answered, while the
// server replaces their control connection with a TLS one. Run it with -race.
func TestClientContextWhileClientStartsTLS(t *testing.T) {
	driver := &TestServerDriver{TLS: true}
	server := NewTestServerWithTestDriver(t, driver)

	var waitGroup sync.WaitGroup

	for _, control := range dialControlConns(t, server, driver, 50) {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			_, err := control.conn.Write([]byte("AUTH TLS\r\n"))
			if !assert.NoError(t, err) {
				return
			}

			reply, err := control.reader.ReadString('\n')
			if !assert.NoError(t, err) {
				return
			}

			assert.Equal(t, "234 AUTH command ok. Expecting TLS Negotiation.\r\n", reply)

			assert.Equal(t, control.conn.LocalAddr().String(), control.context.RemoteAddr().String())
			assert.Equal(t, control.conn.RemoteAddr().String(), control.context.LocalAddr().String())
			assert.NoError(t, control.context.Close())
		}()
	}

	waitGroup.Wait()
}

// TestCloseWhileClientStartsTransfer closes clients from another goroutine while their LIST starts: Close sets the
// transfer's abort flag, which the client's goroutine resets when a transfer command starts. Run it with -race.
func TestCloseWhileClientStartsTransfer(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)
	controls := dialControlConns(t, server, driver, 50)

	for _, control := range controls {
		requireControlReply(t, control, "USER "+authUser, StatusUserOK)
		requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)
	}

	var waitGroup sync.WaitGroup

	for _, control := range controls {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			// Once NOOP is answered, the client's goroutine reads LIST
			_, err := control.conn.Write([]byte("NOOP\r\nLIST\r\n"))
			if !assert.NoError(t, err) {
				return
			}

			reply, err := control.reader.ReadString('\n')
			if !assert.NoError(t, err) {
				return
			}

			assert.Equal(t, "200 OK\r\n", reply)
			assert.NoError(t, control.context.Close())
		}()
	}

	waitGroup.Wait()
}

// pendingDataConnection is the wait of a transfer for its data connection
type pendingDataConnection struct {
	waiting  <-chan struct{} // closed once the transfer waits
	released <-chan struct{} // closed once the wait is stopped, nil if not reported
	peer     net.Conn        // client end of a data connection that the server must close, with a read deadline
}

type pendingTransferCase struct {
	name     string
	settings func() *Settings
	// prepare sends PASV or PORT and returns the wait of the transfer for its data connection
	prepare func(t *testing.T, control controlConn) pendingDataConnection
}

func passiveSettings() *Settings { return &Settings{} }

// multiplexedSettings shares a single passive port between clients: port 0, the only candidate, makes the shared
// listener bind once to a free port, which PASV reports
func multiplexedSettings() *Settings {
	return &Settings{
		PassiveTransferPortRange:        &PortRange{Start: 0, End: 0},
		PassiveTransferPortMultiplexing: true,
	}
}

func activeSettings() *Settings { return &Settings{ActiveTransferPortNon20: true} }

func pendingTransferCases() []pendingTransferCase {
	return []pendingTransferCase{
		{name: "passive", settings: passiveSettings, prepare: awaitPassiveDataConnection},
		{name: "passive-multiplexed", settings: multiplexedSettings, prepare: awaitPassiveDataConnection},
		{name: "active", settings: activeSettings, prepare: awaitActiveDataConnection},
	}
}

// startPendingTransfer logs a client in and sends LIST, which waits for its data connection
func startPendingTransfer(t *testing.T, testCase pendingTransferCase) (controlConn, pendingDataConnection) {
	t.Helper()

	driver := &TestServerDriver{Settings: testCase.settings()}
	server := NewTestServerWithTestDriver(t, driver)
	control := dialControlConns(t, server, driver, 1)[0]

	requireControlReply(t, control, "USER "+authUser, StatusUserOK)
	requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)

	pending := testCase.prepare(t, control)

	_, err := control.conn.Write([]byte("LIST\r\n"))
	require.NoError(t, err)

	requireSignal(t, pending.waiting, "LIST does not wait for the data connection")

	return control, pending
}

// TestCloseWhileTransferWaitsForDataConnection closes a client, as a driver kicking it does, while its LIST waits
// for the data connection. Close must not wait for ConnectionTimeout, the client must get no 425 before its
// control connection is closed, and a data connection made as Close cancels the wait must be closed. Close closes
// the transfer itself once it gets transferMu: this doesn't cover the discard in TransferOpen's cancel path.
func TestCloseWhileTransferWaitsForDataConnection(t *testing.T) {
	testCases := append(pendingTransferCases(),
		pendingTransferCase{name: "passive-checking", settings: passiveSettings, prepare: awaitPassiveConnectionCheck},
		pendingTransferCase{name: "active-connected", settings: activeSettings, prepare: awaitConnectedActiveDial},
	)

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			control, pending := startPendingTransfer(t, testCase)

			requireCloseWithoutReply(t, control)

			if pending.peer != nil {
				_, err := pending.peer.Read(make([]byte, 1))
				require.ErrorIs(t, err, io.EOF, "the data connection must be closed")
			}
		})
	}
}

// TestClientDropWhileTransferWaitsForDataConnection closes the control connection from the client side while its
// LIST waits for the data connection: the wait must stop at once, not after ConnectionTimeout.
func TestClientDropWhileTransferWaitsForDataConnection(t *testing.T) {
	for _, testCase := range pendingTransferCases() {
		t.Run(testCase.name, func(t *testing.T) {
			control, pending := startPendingTransfer(t, testCase)

			require.NoError(t, control.conn.Close())

			requireSignal(t, pending.released, "the transfer still waits for its data connection")
		})
	}
}

// TestCloseReleasesMultiplexedReservation closes a client whose LIST waits for its data connection on the single
// shared passive port: another client from the same IP can reserve the port right after Close.
func TestCloseReleasesMultiplexedReservation(t *testing.T) {
	driver := &TestServerDriver{Settings: multiplexedSettings()}
	server := NewTestServerWithTestDriver(t, driver)
	controls := dialControlConns(t, server, driver, 2)

	for _, control := range controls {
		requireControlReply(t, control, "USER "+authUser, StatusUserOK)
		requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)
	}

	pending := awaitPassiveDataConnection(t, controls[0])

	_, err := controls[0].conn.Write([]byte("LIST\r\n"))
	require.NoError(t, err)

	requireSignal(t, pending.waiting, "LIST does not wait for the data connection")

	// The port is reserved for this IP until the wait ends
	requireControlReply(t, controls[1], "PASV", StatusServiceNotAvailable)

	requireCloseWithoutReply(t, controls[0])

	requireControlReply(t, controls[1], "PASV", StatusEnteringPASV)
}

// TestCloseWhileCommandWaitsForTransfer closes a client while a command sent after LIST waits for the LIST's
// transfer, itself waiting for its data connection: once Close aborted the transfer, the command must not run or
// reply. ABOR and STAT wait for transferMu, the others for the transfer to end.
func TestCloseWhileCommandWaitsForTransfer(t *testing.T) {
	for _, command := range []string{"DELE file.txt", "ABOR", "STAT", "QUIT"} {
		t.Run(command, func(t *testing.T) {
			received := &lineReceivedHandler{line: command, received: make(chan struct{})}
			testDriver := &TestServerDriver{Debug: true}
			testDriver.Init()
			driver := &quitRecordingDriver{TestServerDriver: testDriver}
			server := NewTestServerWithDriverAndLogger(t, driver, slog.New(received))

			filePath := filepath.Join(testDriver.serverDir, "file.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("content"), 0o600))

			control := dialControlConns(t, server, testDriver, 1)[0]

			requireControlReply(t, control, "USER "+authUser, StatusUserOK)
			requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)

			pending := awaitPassiveDataConnection(t, control)

			_, err := control.conn.Write([]byte("LIST\r\n"))
			require.NoError(t, err)

			// Sent once the transfer holds transferMu: ABOR and STAT, run at once, would otherwise reply before Close
			requireSignal(t, pending.waiting, "LIST does not wait for the data connection")

			_, err = control.conn.Write([]byte(command + "\r\n"))
			require.NoError(t, err)

			// The command is read before Close: it is not dropped as a line read once the connection is closed
			requireSignal(t, received.received, "the command is not received")

			requireCloseWithoutReply(t, control)

			// The client's goroutine ends after the command, if it runs
			waitClientsGone(t, testDriver)

			require.FileExists(t, filePath, "the command must not run after Close")
			require.Zero(t, driver.quits.Load(), "the command must not run after Close")
		})
	}
}

// TestCloseBeforeTransferCommandStarts closes a client once its STOR passed the check made after the wait for the
// previous transfer, but before the transfer starts: it must not start, the driver must not open the file.
func TestCloseBeforeTransferCommandStarts(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)
	control := dialControlConns(t, server, driver, 1)[0]

	requireControlReply(t, control, "USER "+authUser, StatusUserOK)
	requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)

	handler, ok := control.context.(*clientHandler)
	require.True(t, ok)

	// setLastCommand locks paramsMutex between that check and the start of the transfer: held here for reading, it
	// stops STOR there, and TryRLock fails once STOR waits for it
	handler.paramsMutex.RLock()

	release := sync.OnceFunc(handler.paramsMutex.RUnlock)
	defer release()

	_, err := control.conn.Write([]byte("STOR file.txt\r\n"))
	require.NoError(t, err)

	timeout := time.After(10 * time.Second)

	for handler.paramsMutex.TryRLock() {
		handler.paramsMutex.RUnlock()

		select {
		case <-timeout:
			require.FailNow(t, "STOR does not reach setLastCommand")
		default:
			runtime.Gosched()
		}
	}

	requireCloseWithoutReply(t, control)
	release()

	waitClientsGone(t, driver)
	// A transfer goroutine is added before the client's goroutine ends
	handler.transferWg.Wait()

	require.Nil(t, driver.getTransferFile("/file.txt"), "the driver must not open the file after Close")
}

// TestTransferOpenAfterCloseCanceled calls TransferOpen once Close canceled closeCtx, before Close gets
// transferMu, as a transfer getting the lock first does: no reply, not even the one saying that TLS is required.
func TestTransferOpenAfterCloseCanceled(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)
	control := dialControlConns(t, server, driver, 1)[0]

	requireControlReply(t, control, "USER "+authUser, StatusUserOK)
	requireControlReply(t, control, "PASS "+authPass, StatusUserLoggedIn)
	requireControlReply(t, control, "PASV", StatusEnteringPASV)
	// PASV replies before it installs the transfer: NOOP waits for it
	requireControlReply(t, control, "NOOP", StatusOK)
	require.NoError(t, control.context.SetTLSRequirement(MandatoryEncryption))

	handler, ok := control.context.(*clientHandler)
	require.True(t, ok)

	handler.cancelClose()

	_, err := handler.TransferOpen("LIST")
	require.ErrorIs(t, err, errTransferCanceled)

	requireCloseWithoutReply(t, control)
}

// requireSignal waits for signal to be closed, well below the default ConnectionTimeout of 30 seconds
func requireSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		require.FailNow(t, failure)
	}
}

// requireCloseWithoutReply closes control's client and checks that Close doesn't wait for the data connection,
// and that the client gets no reply before its control connection is closed
func requireCloseWithoutReply(t *testing.T, control controlConn) {
	t.Helper()

	closed := make(chan error, 1)

	go func() { closed <- control.context.Close() }()

	// Well below the default ConnectionTimeout of 30 seconds
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "Close waits for the data connection")
	}

	require.NoError(t, control.conn.SetReadDeadline(time.Now().Add(10*time.Second)))

	reply, err := control.reader.ReadString('\n')
	require.ErrorIs(t, err, io.EOF)
	require.Empty(t, reply, "the client must get no reply")
}

// requireControlReply sends command on control and checks the code of its one-line reply
func requireControlReply(t *testing.T, control controlConn, command string, expectedCode int) {
	t.Helper()

	_, err := control.conn.Write([]byte(command + "\r\n"))
	require.NoError(t, err)

	reply, err := control.reader.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(reply, fmt.Sprintf("%d ", expectedCode)), reply)
}

// editPendingTransfer calls edit with the transfer handler PASV or PORT installed for control's client
func editPendingTransfer(t *testing.T, control controlConn, edit func(transfer transferHandler)) {
	t.Helper()

	handler, ok := control.context.(*clientHandler)
	require.True(t, ok)

	handler.transferMu.Lock()
	defer handler.transferMu.Unlock()

	edit(handler.transfer)
}

// replacePassiveListener sends PASV, replaces the listener of the transfer it prepares with what wrap returns, and
// returns the passive port
func replacePassiveListener(t *testing.T, control controlConn, wrap func(listener net.Listener) net.Listener) int {
	t.Helper()

	requireControlReply(t, control, "PASV", StatusEnteringPASV)
	// PASV replies before it installs the transfer: NOOP waits for it
	requireControlReply(t, control, "NOOP", StatusOK)

	var port int

	editPendingTransfer(t, control, func(transfer transferHandler) {
		passive, ok := transfer.(*passiveTransferHandler)
		require.True(t, ok)

		passive.listener = wrap(passive.listener)
		port = passive.Port
	})

	return port
}

// awaitPassiveDataConnection sends PASV. The transfer waits once it accepts on its listener, and the wait is
// stopped once the listener is closed.
func awaitPassiveDataConnection(t *testing.T, control controlConn) pendingDataConnection {
	t.Helper()

	listener := &reportingListener{accepting: make(chan struct{}), closed: make(chan struct{})}

	replacePassiveListener(t, control, func(wrapped net.Listener) net.Listener {
		listener.Listener = wrapped

		return listener
	})

	return pendingDataConnection{waiting: listener.accepting, released: listener.closed}
}

// awaitPassiveConnectionCheck sends PASV and makes the data connection. The transfer waits once it checks the
// connection: its RemoteAddr reads a header that never comes.
func awaitPassiveConnectionCheck(t *testing.T, control controlConn) pendingDataConnection {
	t.Helper()

	listener := &headerReadingListener{reading: make(chan struct{})}

	port := replacePassiveListener(t, control, func(wrapped net.Listener) net.Listener {
		listener.Listener = wrapped

		return listener
	})

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	peer, err := dialer.DialContext(t.Context(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	t.Cleanup(func() { _ = peer.Close() })

	require.NoError(t, peer.SetReadDeadline(time.Now().Add(20*time.Second)))

	return pendingDataConnection{waiting: listener.reading, peer: peer}
}

// awaitActiveDataConnection sends PORT and makes the transfer's dial fail once it is canceled
func awaitActiveDataConnection(t *testing.T, control controlConn) pendingDataConnection {
	t.Helper()

	return awaitActiveDial(t, control, func(ctx context.Context) (net.Conn, error) { return nil, ctx.Err() })
}

// awaitConnectedActiveDial sends PORT and makes the transfer's dial succeed once it is canceled, as a dial that
// connects just as the wait is canceled
func awaitConnectedActiveDial(t *testing.T, control controlConn) pendingDataConnection {
	t.Helper()

	serverEnd, clientEnd := net.Pipe()

	t.Cleanup(func() {
		_ = serverEnd.Close()
		_ = clientEnd.Close()
	})

	require.NoError(t, clientEnd.SetReadDeadline(time.Now().Add(20*time.Second)))

	pending := awaitActiveDial(t, control, func(context.Context) (net.Conn, error) { return serverEnd, nil })
	pending.peer = clientEnd

	return pending
}

// awaitActiveDial sends PORT and makes the transfer's dial wait until it is canceled, then return what canceled
// returns. The transfer waits once it dials, and the wait is stopped once the dial returns.
func awaitActiveDial(
	t *testing.T,
	control controlConn,
	canceled func(ctx context.Context) (net.Conn, error),
) pendingDataConnection {
	t.Helper()

	// Nothing has to listen on this address: the dial below never connects
	requireControlReply(t, control, "PORT 127,0,0,1,4,1", StatusOK)

	dialing := make(chan struct{})
	released := make(chan struct{})

	editPendingTransfer(t, control, func(transfer transferHandler) {
		active, ok := transfer.(*activeTransferHandler)
		require.True(t, ok)

		active.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(dialing)
			defer close(released)

			// Nothing else ends the wait: if the context given to Open did not reach the dial, the test would fail
			<-ctx.Done()

			return canceled(ctx)
		}
	})

	return pendingDataConnection{waiting: dialing, released: released}
}

// reportingListener closes accepting on its first Accept, and closed once it is closed
type reportingListener struct {
	net.Listener
	accepting  chan struct{}
	acceptOnce sync.Once
	closed     chan struct{}
	closeOnce  sync.Once
}

func (l *reportingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepting) })

	return l.Listener.Accept()
}

func (l *reportingListener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.closed) })

	return err
}

// headerReadingListener accepts headerReadingConn connections
type headerReadingListener struct {
	net.Listener
	reading     chan struct{} // closed once the RemoteAddr of a connection reads
	readingOnce sync.Once
}

func (l *headerReadingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &headerReadingConn{Conn: conn, listener: l}, nil
}

// headerReadingConn reads a header in its first RemoteAddr, as a passive wrapper reading the PROXY protocol header
// does
type headerReadingConn struct {
	net.Conn
	listener   *headerReadingListener
	headerOnce sync.Once
}

func (c *headerReadingConn) RemoteAddr() net.Addr {
	c.headerOnce.Do(func() {
		c.listener.readingOnce.Do(func() { close(c.listener.reading) })

		_, _ = c.Read(make([]byte, 1))
	})

	return c.Conn.RemoteAddr()
}

// quitRecordingDriver counts the calls to QuitMessage
type quitRecordingDriver struct {
	*TestServerDriver
	quits atomic.Int32
}

func (d *quitRecordingDriver) QuitMessage() string {
	d.quits.Add(1)

	return "Goodbye"
}

// lineReceivedHandler closes received once the client handler logs, in debug, that it received line
type lineReceivedHandler struct {
	line     string
	received chan struct{}
	once     sync.Once
}

func (h *lineReceivedHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

//nolint:gocritic // slog.Handler requires the record by value
func (h *lineReceivedHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message != "Received line" {
		return nil
	}

	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "line" && attr.Value.String() == h.line {
			h.once.Do(func() { close(h.received) })
		}

		return true
	})

	return nil
}

func (h *lineReceivedHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *lineReceivedHandler) WithGroup(string) slog.Handler {
	return h
}

func TestClientContextConcurrency(t *testing.T) {
	driver := &TestServerDriver{}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	done := make(chan bool, 1)
	connected := make(chan bool, 1)

	go func() {
		_, err := client.Getwd()
		assert.NoError(t, err)
		connected <- true

		counter := 0

		for counter < 100 {
			_, err := client.Getwd()
			assert.NoError(t, err)

			counter++
		}

		done <- true
	}()

	<-connected

	isDone := false
	for !isDone {
		info := driver.GetClientsInfo()
		assert.Len(t, info, 1)

		select {
		case <-done:
			isDone = true
		default:
		}
	}
}

type multilineMessage struct {
	message       string
	expectedLines []string
}

func TestMultiLineMessages(t *testing.T) {
	testMultilines := []multilineMessage{
		{
			message:       "single line",
			expectedLines: []string{"single line"},
		},
		{
			message:       "",
			expectedLines: []string{""},
		},
		{
			message:       "first line\r\nsecond line\r\n",
			expectedLines: []string{firstLine, secondLine},
		},
		{
			message:       "first line\nsecond line\n",
			expectedLines: []string{firstLine, secondLine},
		},
		{
			message:       "first line\rsecond line",
			expectedLines: []string{"first line\rsecond line"},
		},
		{
			message: `first line

second line

`,
			expectedLines: []string{firstLine, "", secondLine, ""},
		},
	}

	for _, msg := range testMultilines {
		lines := getMessageLines(msg.message)
		if len(lines) != len(msg.expectedLines) {
			t.Errorf("unexpected number of lines got: %v want: %v", len(lines), len(msg.expectedLines))
		}

		for _, line := range lines {
			if !isStringInSlice(line, msg.expectedLines) {
				t.Errorf("unexpected line %#v", line)
			}
		}
	}
}

func isStringInSlice(s string, list []string) bool {
	return slices.Contains(list, s)
}

func TestUnknownCommand(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	cmd := "UNSUPPORTED"
	rc, response, err := raw.SendCommand("%s", cmd)
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
	require.Equal(t, fmt.Sprintf("Unknown command %#v", cmd), response)
}

// testNetConn implements net.Conn interface
type testNetConn struct {
	remoteAddr net.Addr
	closed     bool
}

func (*testNetConn) Read(_ []byte) (int, error) {
	return 0, nil
}

func (*testNetConn) Write(_ []byte) (int, error) {
	return 0, nil
}

func (c *testNetConn) Close() error {
	c.closed = true

	return nil
}

func (*testNetConn) LocalAddr() net.Addr {
	return nil
}

func (c *testNetConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (*testNetConn) SetDeadline(_ time.Time) error {
	return nil
}

func (*testNetConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (*testNetConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

// testNetListener implements net.Listener interface. It yields the connections of conns in order, then conn
// forever if set, then an error.
type testNetListener struct {
	conns []net.Conn
	conn  net.Conn
}

func (l *testNetListener) Accept() (net.Conn, error) {
	if len(l.conns) > 0 {
		conn := l.conns[0]
		l.conns = l.conns[1:]

		return conn, nil
	}

	if l.conn != nil {
		return l.conn, nil
	}

	return nil, &net.AddrError{}
}

func (*testNetListener) Close() error {
	return nil
}

func (*testNetListener) Addr() net.Addr {
	return nil
}

func TestDataConnectionRequirements(t *testing.T) {
	req := require.New(t)
	controlConnIP := net.ParseIP("192.168.1.1")

	cltHandler := clientHandler{
		conn: &testNetConn{
			remoteAddr: &net.TCPAddr{IP: controlConnIP, Port: 21},
		},
		server: &FtpServer{
			settings: &Settings{
				PasvConnectionsCheck:   IPMatchRequired,
				ActiveConnectionsCheck: IPMatchRequired,
			},
		},
	}

	err := cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	req.NoError(err) // ip match

	err = cltHandler.checkDataConnectionRequirement(net.ParseIP("192.168.1.2"), DataChannelActive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "does not match control connection ip address")
	}

	cltHandler.conn = &testNetConn{
		remoteAddr: &net.IPAddr{IP: controlConnIP},
	}

	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	req.Error(err)

	// nil remote address
	cltHandler.conn = &testNetConn{}
	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelActive)
	req.Error(err)

	// invalid IP
	cltHandler.conn = &testNetConn{
		remoteAddr: &net.TCPAddr{IP: nil, Port: 21},
	}

	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid remote IP")
	}

	// invalid setting
	cltHandler.server.settings.PasvConnectionsCheck = 100
	err = cltHandler.checkDataConnectionRequirement(controlConnIP, DataChannelPassive)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "unhandled data connection requirement")
	}
}

func TestExtraData(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	info := driver.GetClientsInfo()
	require.Len(t, info, 1)

	for k, v := range info {
		ccInfo, ok := v.(map[string]any)
		require.True(t, ok)
		extra, ok := ccInfo["extra"].(uint32)
		require.True(t, ok)
		require.Equal(t, k, extra)
	}
}

var (
	errClosedConn     = net.ErrClosed
	errConnReset      = errors.New("connection reset by peer")
	errOther          = errors.New("some other error")
	errWrappedClosed  = fmt.Errorf("failed: %w", errClosedConn)
	errClosedConnText = errors.New("use of closed network connection")
	errRandomError    = errors.New("some random error")
)

func TestIsClosedConnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil error", err: nil, expected: false},
		{name: "net.ErrClosed", err: errClosedConn, expected: true},
		{name: "connection reset by peer", err: errConnReset, expected: true},
		{name: "wrapped closed connection error", err: errWrappedClosed, expected: true},
		{name: "other error", err: errOther, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := isClosedConnError(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// mockNetError implements net.Error for testing
type mockNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *mockNetError) Error() string   { return e.msg }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

// testHandleCommandsStreamErrorCase represents a test case for handleCommandsStreamError.
type testHandleCommandsStreamErrorCase struct {
	name               string
	err                error
	debug              bool
	expectedDisconnect bool
}

// getHandleCommandsStreamErrorTestCases returns all test cases for handleCommandsStreamError.
func getHandleCommandsStreamErrorTestCases() []testHandleCommandsStreamErrorCase {
	return []testHandleCommandsStreamErrorCase{
		{name: "EOF with debug", err: io.EOF, debug: true, expectedDisconnect: true},
		{name: "EOF without debug", err: io.EOF, debug: false, expectedDisconnect: true},
		{name: "non-net.Error closed with debug", err: errClosedConnText, debug: true, expectedDisconnect: true},
		{name: "non-net.Error closed without debug", err: errClosedConnText, debug: false, expectedDisconnect: true},
		{name: "non-net.Error reset", err: errConnReset, debug: true, expectedDisconnect: true},
		{name: "net.Error closed with debug", err: &mockNetError{msg: "use of closed network connection"},
			debug: true, expectedDisconnect: true},
		{name: "net.Error closed without debug", err: &mockNetError{msg: "use of closed network connection"},
			debug: false, expectedDisconnect: true},
		{name: "net.Error other", err: &mockNetError{msg: "other network error"}, debug: false, expectedDisconnect: true},
		{name: "generic error", err: errRandomError, debug: false, expectedDisconnect: true},
	}
}

// TestHandleCommandsStreamError tests the handleCommandsStreamError function
// with various error types to ensure proper handling of closed connections.
func TestHandleCommandsStreamError(t *testing.T) {
	t.Parallel()

	//nolint:sloglint // DiscardHandler requires Go 1.23+
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, testCase := range getHandleCommandsStreamErrorTestCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			buf := bytes.Buffer{}
			server := &FtpServer{
				settings: &Settings{IdleTimeout: 0},
				Logger:   logger,
			}
			handler := &clientHandler{
				writer: bufio.NewWriter(&buf),
				server: server,
				logger: logger,
				debug:  testCase.debug,
			}

			result := handler.handleCommandsStreamError(testCase.err)
			assert.Equal(t, testCase.expectedDisconnect, result)
		})
	}
}

func TestDeflateReadWriterFlush(t *testing.T) {
	t.Parallel()

	// Create a buffer to write to
	buf := &mockReadWriter{}

	// Create deflate transfer
	deflate, err := newDeflateTransfer(buf, 5)
	require.NoError(t, err)

	// Write some data
	data := []byte("test data for deflate")
	n, err := deflate.Write(data)
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	// Test Flush
	err = deflate.Flush()
	require.NoError(t, err)

	// Verify data was written to buffer
	require.Positive(t, buf.writeCount)
}

func TestNewDeflateTransferInvalidLevel(t *testing.T) {
	t.Parallel()

	buf := &mockReadWriter{}

	// Test with invalid compression level (valid range is -2 to 9)
	_, err := newDeflateTransfer(buf, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not create deflate writer")
}

// mockReadWriter is a simple mock for testing
type mockReadWriter struct {
	writeCount int
	readCount  int
}

func (m *mockReadWriter) Write(p []byte) (int, error) {
	m.writeCount++

	return len(p), nil
}

func (m *mockReadWriter) Read(_ []byte) (int, error) {
	m.readCount++

	return 0, nil
}

// TestImmediateClientDisconnect tests that when a client connects and immediately
// disconnects (before sending any FTP commands), the server handles it gracefully
// without logging errors. This is common behavior for FTP clients that probe
// connections or do quick connectivity checks.
func TestImmediateClientDisconnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		debug bool
	}{
		{name: "with debug", debug: true},
		{name: "without debug", debug: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := NewTestServer(t, testCase.debug)
			dialer := &net.Dialer{Timeout: 5 * time.Second}

			// Connect and immediately close without sending any commands
			conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
			require.NoError(t, err)

			// Read the welcome message to ensure the server has started handling us
			buf := make([]byte, 1024)
			_, err = conn.Read(buf)
			require.NoError(t, err)

			// Close immediately without sending any commands
			err = conn.Close()
			require.NoError(t, err)

			// Give the server time to process the disconnect
			time.Sleep(100 * time.Millisecond)

			// Verify server is still functional by connecting again
			newConn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
			require.NoError(t, err)

			defer func() { _ = newConn.Close() }()

			_, err = newConn.Read(buf)
			require.NoError(t, err)
			require.Contains(t, string(buf), "220")
		})
	}
}

// TestMultipleImmediateDisconnects tests that the server handles many rapid
// connect/disconnect cycles gracefully (simulating probe traffic).
func TestMultipleImmediateDisconnects(t *testing.T) {
	t.Parallel()

	server := NewTestServer(t, true)
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	for range 10 {
		conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
		require.NoError(t, err)

		// Read welcome message
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)

		// Immediate close
		_ = conn.Close()
	}

	// Small delay to let server process all disconnects
	time.Sleep(200 * time.Millisecond)

	// Verify server still works
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	_, err = client.ReadDir("/")
	require.NoError(t, err)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestRedactLine(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "PASS ****", redactLine("PASS secret"))
	assert.Equal(t, "pass ****", redactLine("pass secret with spaces"))
	assert.Equal(t, "PASS", redactLine("PASS"))
	assert.Equal(t, "USER acme", redactLine("USER acme"))
	assert.Equal(t, "CWD /dir", redactLine("CWD /dir"))
	assert.Equal(t, "****", redactParam("PASS", "secret"))
	assert.Equal(t, "/dir", redactParam("CWD", "/dir"))
}

func TestDebugLogRedactsPassword(t *testing.T) {
	const password = "s3cr3t-acme-password"

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	driver := &TestServerDriver{Debug: true}
	driver.AuthProvider = func(user, pass string) (ClientDriver, error) {
		if user == "acme" && pass == password {
			return NewTestClientDriver(driver), nil
		}

		return nil, errInvalidChownUser
	}
	driver.Init()

	server := NewTestServerWithDriverAndLogger(t, driver, logger)

	client, err := goftp.DialConfig(goftp.Config{User: "acme", Password: password}, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	_, _, err = raw.SendCommand("CWD /dir")
	require.NoError(t, err)

	output := logs.String()
	require.NotContains(t, output, password)
	require.Contains(t, output, `line="PASS ****"`)
	require.Contains(t, output, `line="USER acme"`)
	require.Contains(t, output, `line="CWD /dir"`)
}
