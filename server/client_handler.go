package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type clientHandler struct {
	id          uint32               // ID of the client
	transferTLS bool                 // Use TLS for transfer connection
	server      *FtpServer           // Server on which the connection was accepted
	driver      ClientHandlingDriver // Client handling driver
	conn        net.Conn             // TCP connection
	reader      *bufio.Reader        // Reader on the TCP connection
	user        string               // Authenticated user
	path        string               // Current path
	command     string               // Command received on the connection
	param       string               // Param of the FTP command
	connectedAt time.Time            // Date of connection
	ctxRnfr     string               // Rename from
	ctxRest     int64                // Restart point
	transfer    transferHandler      // Transfer connection (only passive is implemented at this stage)
	logger      *logrus.Entry        // Client handler logging
	mtx         sync.Mutex
	done        chan struct{}
}

// newClientHandler initializes a client handler when someone connects
func (server *FtpServer) newClientHandler(conn net.Conn, id uint32, log *logrus.Entry) *clientHandler {
	ch := &clientHandler{
		server:      server,
		conn:        conn,
		id:          id,
		reader:      bufio.NewReader(conn),
		connectedAt: time.Now().UTC(),
		path:        "/",
		logger:      log.WithFields(logrus.Fields{"clientId": id, "clientIp": conn.RemoteAddr()}),
		done:        make(chan struct{}),
	}

	ch.logger.WithField(logKeyAction, "ftp.connected").Info("FTP Client connected")
	go ch.handleCommands()

	return ch
}

// Path provides the current working directory of the client
func (c *clientHandler) Path() string {
	return c.path
}

// SetPath changes the current working directory
func (c *clientHandler) SetPath(path string) {
	c.path = path
}

// Debug defines if we will list all interaction
func (c *clientHandler) Debug() bool {
	return c.logger.Logger.Level == logrus.DebugLevel
}

// SetDebug changes the debug flag
func (c *clientHandler) SetDebug(debug bool) {
	if debug {
		c.logger.Logger.SetLevel(logrus.DebugLevel)
	} else {
		c.logger.Logger.SetLevel(logrus.InfoLevel)
	}
}

// ID provides the client's ID
func (c *clientHandler) ID() uint32 {
	return c.id
}

// RemoteAddr returns the remote network address.
func (c *clientHandler) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// LocalAddr returns the local network address.
func (c *clientHandler) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// Close closes all open connections for the client.
func (c *clientHandler) Close() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	select {
	case <-c.done:
		// Already closed
		return
	default:
	}

	c.logger.WithField(logKeyAction, "ftp.disconnected").Info("FTP Client disconnected")
	if err := c.conn.Close(); err != nil {
		logError(c.logger.WithField(logKeyAction, "ftp.close_error"), "Network close error ", err)
	}

	c.transferCloseLocked()
	close(c.done)
}

func (c *clientHandler) isDone() bool {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// end cleans up the client resources and then notifies the server.
func (c *clientHandler) end() {
	c.Close()
	c.server.driver.UserLeft(c)
	c.server.clientDeparture(c)
}

func (c *clientHandler) sendWelcome() error {
	msg, err := c.server.driver.WelcomeUser(c)
	if err != nil {
		c.writeMessage(StatusServiceNotAvailable, err.Error()) // nolint: errcheck
		return err
	}

	return c.writeMessage(StatusServiceReady, msg)
}

// handleCommands reads the stream of commands
func (c *clientHandler) handleCommands() {
	defer c.end()
	if err := c.sendWelcome(); err != nil {
		logError(c.logger.WithField(logKeyAction, "ftp.send_welcome"), "Send welcome error ", err)
		return
	}

	for !c.server.Stopped() && !c.isDone() {
		if c.server.settings.IdleTimeout > 0 {
			c.setDeadline(time.Now().Add(time.Duration(c.server.settings.IdleTimeout) * time.Second))
		}

		line, err := c.reader.ReadString('\n')
		if c.server.Stopped() || c.isDone() {
			return
		}

		if err != nil {
			c.handleReadError(err)
			return
		}

		line = strings.TrimRight(line, "\r\n")
		c.logger.WithFields(logrus.Fields{logKeyAction: "ftp.cmd_recv", "line": line}).Debug("FTP RECV")
		if err := c.handleCommand(line); err != nil {
			c.logger.WithField(logKeyAction, "ftp.handle_cmd").Errorf("Command %q failed (%v)", line, err)
			return
		}
	}
}

func (c *clientHandler) setDeadline(deadline time.Time) {
	if err := c.conn.SetDeadline(deadline); err != nil {
		logError(c.logger.WithField(logKeyAction, "ftp.set_deadline"), "Set deadline error ", err)
	}
}

func (c *clientHandler) handleReadError(err error) {
	switch err := err.(type) {
	case net.Error:
		if err.Timeout() {
			// We have to extend the deadline now
			c.setDeadline(time.Now().Add(time.Minute))
			l := c.logger.WithField(logKeyAction, "ftp.idle_timeout")
			logError(l, "IDLE timeout ", err)
			if err := c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("command timeout (%d seconds): closing control connection", c.server.settings.IdleTimeout)); err != nil {
				logError(l, "Write failure: ", err)
			}
			return
		}
		logError(c.logger.WithField(logKeyAction, "ftp.net_error"), "Network error ", err)
	default:
		logError(c.logger.WithField(logKeyAction, "ftp.read_error"), "Read error ", err)
	}
}

// handleCommand takes care of executing the received line
func (c *clientHandler) handleCommand(line string) (err error) {
	command, param := parseLine(line)
	c.command = strings.ToUpper(command)
	c.param = param

	cmdDesc := commandsMap[c.command]
	if cmdDesc == nil {
		return c.writeMessage(StatusSyntaxErrorNotRecognised, "Unknown command")
	}

	if c.driver == nil && !cmdDesc.Open {
		return c.writeMessage(StatusNotLoggedIn, "Please login with USER and PASS")
	}

	// Let's prepare to recover in case there's a command error
	defer func() {
		if r := recover(); r != nil {
			// TODO(steve): Do we need to check if we have already sent a partial reply?
			err = c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Internal error: %s", r))
		}
	}()

	return cmdDesc.Fn(c)
}

func (c *clientHandler) writeLine(line string) error {
	c.logger.WithFields(logrus.Fields{logKeyAction: "ftp.cmd_send", "line": line}).Debug("FTP SEND")
	_, err := c.conn.Write([]byte(line + "\r\n"))

	return err
}

func (c *clientHandler) writeMessage(code int, message string) error {
	return c.writeLine(fmt.Sprintf("%d %s", code, message))
}

func (c *clientHandler) setTransfer(transfer transferHandler) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.transfer = transfer
}

func (c *clientHandler) TransferOpen() (net.Conn, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if c.transfer == nil {
		if err := c.writeMessage(StatusActionNotTaken, "No passive connection declared"); err != nil {
			return nil, err
		}
		return nil, errors.New("no passive connection declared")
	}

	if err := c.writeMessage(StatusFileStatusOK, "Using transfer connection"); err != nil {
		return nil, err
	}
	conn, err := c.transfer.Open()
	if err == nil {
		c.logger.WithFields(logrus.Fields{logKeyAction: "ftp.transfer_open", "remoteAddr": conn.RemoteAddr(), "localAddr": conn.LocalAddr()}).Debug("FTP Transfer connection opened")
	}
	return conn, err
}

func (c *clientHandler) TransferClose() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.transferCloseLocked()
}

func (c *clientHandler) transferCloseLocked() {
	if c.transfer != nil {
		l := c.logger.WithField(logKeyAction, "ftp.transfer_close")
		if err := c.writeMessage(StatusClosingDataConn, "Closing transfer connection"); err != nil {
			logError(l, "Write failed: ", err)
		}

		if err := c.transfer.Close(); err != nil {
			logError(l, "Close failed: ", err)
		}
		l.Debug("FTP Transfer connection closed")
		c.transfer = nil
	}
}

func parseLine(line string) (string, string) {
	params := strings.SplitN(strings.Trim(line, "\r\n"), " ", 2)
	if len(params) == 1 {
		return params[0], ""
	}
	return params[0], params[1]
}

// logError logs an error if err is not an error we would expect for closed connections.
func logError(l *logrus.Entry, msg string, err error) {
	if closedConnErr(err) {
		return
	}
	l.Error(msg, err)
}

// closedConnErr returns true if the error message indicates use of a closed connection.
func closedConnErr(err error) bool {
	if err == io.EOF {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") || strings.Contains(s, "connection reset by peer")
}
