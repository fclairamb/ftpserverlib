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
	daddy       *FtpServer           // Server on which the connection was accepted
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
		daddy:       server,
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
		c.logger.WithField(logKeyAction, "ftp.close_error").Error("Network close error ", err)
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
	c.daddy.driver.UserLeft(c)
	c.daddy.clientDeparture(c)
}

func (c *clientHandler) sendWelcome() error {
	msg, err := c.daddy.driver.WelcomeUser(c)
	if err != nil {
		if err2 := c.writeMessage(500, err.Error()); err2 != nil {
			c.logger.Error(err2)
		}
		return err
	}

	return c.writeMessage(220, msg)
}

// handleCommands reads the stream of commands
func (c *clientHandler) handleCommands() {
	defer c.end()
	if err := c.sendWelcome(); err != nil {
		c.logger.Error(err)
		return
	}

	for !c.daddy.Stopped() && !c.isDone() {
		if c.daddy.settings.IdleTimeout > 0 {
			c.setDeadline(time.Now().Add(time.Duration(c.daddy.settings.IdleTimeout) * time.Second))
		}

		line, err := c.reader.ReadString('\n')
		if c.daddy.Stopped() || c.isDone() {
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
		c.logger.WithField(logKeyAction, "ftp.set_deadline").Error(err)
	}
}

func (c *clientHandler) handleReadError(err error) {
	switch err := err.(type) {
	case net.Error:
		if err.Timeout() {
			// We have to extend the deadline now
			c.setDeadline(time.Now().Add(time.Minute))
			l := c.logger.WithField(logKeyAction, "ftp.idle_timeout")
			l.Error("IDLE timeout ", err)
			if err := c.writeMessage(421, fmt.Sprintf("command timeout (%d seconds): closing control connection", c.daddy.settings.IdleTimeout)); err != nil {
				l.Error("Write failure: ", err)
			}
			return
		}
		c.logger.WithField(logKeyAction, "ftp.net_error").Error("Network error ", err)
	default:
		if err == io.EOF {
			c.logger.WithField(logKeyAction, "ftp.disconnect").Error("TCP disconnect ", err)
		} else {
			c.logger.WithField(logKeyAction, "ftp.read_error").Error("Read error ", err)
		}
	}
}

// handleCommand takes care of executing the received line
func (c *clientHandler) handleCommand(line string) (err error) {
	command, param := parseLine(line)
	c.command = strings.ToUpper(command)
	c.param = param

	cmdDesc := commandsMap[c.command]
	if cmdDesc == nil {
		return c.writeMessage(500, "Unknown command")
	}

	if c.driver == nil && !cmdDesc.Open {
		return c.writeMessage(530, "Please login with USER and PASS")
	}

	// Let's prepare to recover in case there's a command error
	defer func() {
		if r := recover(); r != nil {
			err = c.writeMessage(500, fmt.Sprintf("Internal error: %s", r))
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
		if err := c.writeMessage(550, "No passive connection declared"); err != nil {
			return nil, err
		}
		return nil, errors.New("no passive connection declared")
	}

	if err := c.writeMessage(150, "Using transfer connection"); err != nil {
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
		if err := c.writeMessage(226, "Closing transfer connection"); err != nil {
			l.Error("Write failed: ", err)
		}

		if err := c.transfer.Close(); err != nil {
			l.Error("Close failed: ", err)
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
