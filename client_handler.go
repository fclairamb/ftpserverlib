// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fclairamb/ftpserverlib/log"
)

// HASHAlgo is the enumerable that represents the supported HASH algorithms
type HASHAlgo int

// Supported hash algorithms
const (
	HASHAlgoCRC32 HASHAlgo = iota
	HASHAlgoMD5
	HASHAlgoSHA1
	HASHAlgoSHA256
	HASHAlgoSHA512
)

var (
	errNoTrasferConnection = errors.New("unable to open transfer: no transfer connection")
	errTLSRequired         = errors.New("unable to open transfer: TLS is required")
)

func getHashMapping() map[string]HASHAlgo {
	mapping := make(map[string]HASHAlgo)
	mapping["CRC32"] = HASHAlgoCRC32
	mapping["MD5"] = HASHAlgoMD5
	mapping["SHA-1"] = HASHAlgoSHA1
	mapping["SHA-256"] = HASHAlgoSHA256
	mapping["SHA-512"] = HASHAlgoSHA512

	return mapping
}

func getHashName(algo HASHAlgo) string {
	hashName := ""
	hashMapping := getHashMapping()

	for k, v := range hashMapping {
		if v == algo {
			hashName = k
		}
	}

	return hashName
}

// nolint: maligned
type clientHandler struct {
	id                uint32          // ID of the client
	server            *FtpServer      // Server on which the connection was accepted
	driver            ClientDriver    // Client handling driver
	conn              net.Conn        // TCP connection
	writer            *bufio.Writer   // Writer on the TCP connection
	reader            *bufio.Reader   // Reader on the TCP connection
	user              string          // Authenticated user
	path              string          // Current path
	clnt              string          // Identified client
	command           string          // Command received on the connection
	connectedAt       time.Time       // Date of connection
	ctxRnfr           string          // Rename from
	ctxRest           int64           // Restart point
	debug             bool            // Show debugging info on the server side
	transferTLS       bool            // Use TLS for transfer connection
	controlTLS        bool            // Use TLS for control connection
	selectedHashAlgo  HASHAlgo        // algorithm used when we receive the HASH command
	logger            log.Logger      // Client handler logging
	transferWg        sync.WaitGroup  // wait group for command that open a transfer connection
	transferMu        sync.Mutex      // this mutex will protect the transfer parameters
	transfer          transferHandler // Transfer connection (passive or active)s
	isTransferOpen    bool            // indicate if the transfer connection is opened
	isTransferAborted bool            // indicate if the transfer was aborted
}

// newClientHandler initializes a client handler when someone connects
func (server *FtpServer) newClientHandler(connection net.Conn, id uint32) *clientHandler {
	p := &clientHandler{
		server:           server,
		conn:             connection,
		id:               id,
		writer:           bufio.NewWriter(connection),
		reader:           bufio.NewReader(connection),
		connectedAt:      time.Now().UTC(),
		path:             "/",
		selectedHashAlgo: HASHAlgoSHA256,
		logger:           server.Logger.With("clientId", id),
	}

	return p
}

func (c *clientHandler) disconnect() {
	if err := c.conn.Close(); err != nil {
		c.logger.Warn(
			"Problem disconnecting a client",
			"err", err,
		)
	}
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
	return c.debug
}

// SetDebug changes the debug flag
func (c *clientHandler) SetDebug(debug bool) {
	c.debug = debug
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

// GetClientVersion returns the identified client, can be empty.
func (c *clientHandler) GetClientVersion() string {
	return c.clnt
}

// HasTLSForControl returns true if the control connection is over TLS
func (c *clientHandler) HasTLSForControl() bool {
	if c.server.settings.TLSRequired == ImplicitEncryption {
		return true
	}

	return c.controlTLS
}

// GetLastCommand returns the last received command
func (c *clientHandler) GetLastCommand() string {
	return c.command
}

func (c *clientHandler) SetCommand(cmd string) {
	c.command = cmd
}

// HasTLSForTransfers returns true if the transfer connection is over TLS
func (c *clientHandler) HasTLSForTransfers() bool {
	if c.server.settings.TLSRequired == ImplicitEncryption {
		return true
	}

	return c.transferTLS
}

func (c *clientHandler) closeTransfer() error {
	var err error
	if c.transfer != nil {
		err = c.transfer.Close()
		c.isTransferOpen = false
		c.transfer = nil

		if c.debug {
			c.logger.Debug("Transfer connection closed")
		}
	}

	return err
}

// Close closes the active transfer, if any, and the control connection
func (c *clientHandler) Close(code int, message string) error {
	c.transferMu.Lock()

	if err := c.closeTransfer(); err != nil {
		c.logger.Warn(
			"Problem closing a transfer on external close request",
			"err", err,
		)
	}

	c.transferMu.Unlock()

	if code > 0 {
		c.writeMessage(code, message)
	}

	if err := c.writer.Flush(); err != nil {
		c.logger.Error("Flush error", "err", err)
	}

	return c.conn.Close()
}

func (c *clientHandler) end() {
	c.server.driver.ClientDisconnected(c)
	c.server.clientDeparture(c)

	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if err := c.closeTransfer(); err != nil {
		c.logger.Warn(
			"Problem closing a transfer",
			"err", err,
		)
	}
}

func (c *clientHandler) isCommandAborted() (aborted bool) {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	aborted = c.isTransferAborted

	return
}

func (c *clientHandler) canOpenTransfer(command string) bool {
	for _, cmd := range transferCommands {
		if cmd == command {
			return true
		}
	}

	return false
}

func (c *clientHandler) isSpecialAttentionCommand(command string) bool {
	for _, cmd := range specialAttentionCommands {
		if cmd == command {
			return true
		}
	}

	return false
}

// HandleCommands reads the stream of commands
func (c *clientHandler) HandleCommands() {
	defer c.end()

	if msg, err := c.server.driver.ClientConnected(c); err == nil {
		c.writeMessage(StatusServiceReady, msg)
	} else {
		c.writeMessage(StatusSyntaxErrorNotRecognised, msg)

		return
	}

	for {
		if c.reader == nil {
			if c.debug {
				c.logger.Debug("Client disconnected", "clean", true)
			}

			return
		}

		// florent(2018-01-14): #58: IDLE timeout: Preparing the deadline before we read
		if c.server.settings.IdleTimeout > 0 {
			if err := c.conn.SetDeadline(
				time.Now().Add(time.Duration(time.Second.Nanoseconds() * int64(c.server.settings.IdleTimeout)))); err != nil {
				c.logger.Error("Network error", "err", err)
			}
		}

		line, err := c.reader.ReadString('\n')

		if err != nil {
			c.handleCommandsStreamError(err)

			return
		}

		if c.debug {
			c.logger.Debug("Received line", "line", line)
		}

		c.handleCommand(line)
	}
}

func (c *clientHandler) handleCommandsStreamError(err error) {
	// florent(2018-01-14): #58: IDLE timeout: Adding some code to deal with the deadline
	switch err := err.(type) {
	case net.Error:
		if err.Timeout() {
			// We have to extend the deadline now
			if err := c.conn.SetDeadline(time.Now().Add(time.Minute)); err != nil {
				c.logger.Error("Could not set read deadline", "err", err)
			}

			c.logger.Info("Client IDLE timeout", "err", err)
			c.writeMessage(
				StatusServiceNotAvailable,
				fmt.Sprintf("command timeout (%d seconds): closing control connection", c.server.settings.IdleTimeout))

			if err := c.writer.Flush(); err != nil {
				c.logger.Error("Flush error", "err", err)
			}

			if err := c.conn.Close(); err != nil {
				c.logger.Error("Close error", "err", err)
			}

			break
		}

		c.logger.Error("Network error", "err", err)
	default:
		if err == io.EOF {
			if c.debug {
				c.logger.Debug("Client disconnected", "clean", false)
			}
		} else {
			c.logger.Error("Read error", "err", err)
		}
	}
}

// handleCommand takes care of executing the received line
func (c *clientHandler) handleCommand(line string) {
	command, param := parseLine(line)
	command = strings.ToUpper(command)

	cmdDesc := commandsMap[command]
	if cmdDesc == nil {
		// Search among commands having a "special semantic". They
		// should be sent by following the RFC-959 procedure of sending
		// Telnet IP/Synch sequence (chr 242 and 255) as OOB data but
		// since many ftp clients don't do it correctly we check the
		// command suffix.
		for _, cmd := range specialAttentionCommands {
			if strings.HasSuffix(command, cmd) {
				cmdDesc = commandsMap[cmd]
				command = cmd

				if cmd == "ABOR" {
					// this way ABOR know about the command to abort
					param = c.GetLastCommand()
				}

				break
			}
		}

		if cmdDesc == nil {
			c.SetCommand(command)
			c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Unknown command %#v", command))

			return
		}
	}

	if c.driver == nil && !cmdDesc.Open {
		c.writeMessage(StatusNotLoggedIn, "Please login with USER and PASS")

		return
	}

	// All commands are serialized except the ones that require special attention.
	// Special attention commands are not executed in a separate goroutine so we can
	// have at most one command that can open a transfer connection and one special
	// attention command running at the same time
	if !c.isSpecialAttentionCommand(command) {
		c.transferWg.Wait()
	}

	c.SetCommand(command)

	if c.canOpenTransfer(command) {
		// these commands will be started in a separate goroutine so
		// they can be aborted.
		// We cannot have two concurrent transfers so also set isTransferAborted
		// to false here.
		// isTransferAborted could remain to true if the previous command is
		// aborted and it does not open a transfer connection, see "transferFile"
		// for details. For this to happen a client should send an ABOR before
		// receiving the StatusFileStatusOK response. This is very unlikely
		// A lock is not required here, we cannot have another concurrent ABOR
		// or transfer active here
		c.isTransferAborted = false

		c.transferWg.Add(1)

		go func(cmd, param string) {
			defer c.transferWg.Done()

			c.executeCommandFn(cmdDesc, cmd, param)
		}(command, param)
	} else {
		c.executeCommandFn(cmdDesc, command, param)
	}
}

func (c *clientHandler) executeCommandFn(cmdDesc *CommandDescription, command, param string) {
	// Let's prepare to recover in case there's a command error
	defer func() {
		if r := recover(); r != nil {
			c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Unhandled internal error: %s", r))
			c.logger.Warn(
				"Internal command handling error",
				"err", r,
				"command", command,
				"param", param,
			)
		}
	}()

	if err := cmdDesc.Fn(c, param); err != nil {
		c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Error: %s", err))
	}
}

func (c *clientHandler) writeLine(line string) {
	if c.debug {
		c.logger.Debug("Sending answer", "line", line)
	}

	if _, err := c.writer.WriteString(fmt.Sprintf("%s\r\n", line)); err != nil {
		c.logger.Warn(
			"Answer couldn't be sent",
			"line", line,
			"err", err,
		)
	}

	if err := c.writer.Flush(); err != nil {
		c.logger.Warn(
			"Couldn't flush line",
			"err", err,
		)
	}
}

func (c *clientHandler) writeMessage(code int, message string) {
	lines := getMessageLines(message)

	for idx, line := range lines {
		if idx < len(lines)-1 {
			c.writeLine(fmt.Sprintf("%d-%s", code, line))
		} else {
			c.writeLine(fmt.Sprintf("%d %s", code, line))
		}
	}
}

func (c *clientHandler) GetTranferInfo() string {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if c.transfer == nil {
		return ""
	}

	return c.transfer.GetInfo()
}

func (c *clientHandler) TransferOpen(info string) (net.Conn, error) {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if c.transfer == nil {
		// a transfer could be aborted before it is opened, in this case no response should be returned
		if c.isTransferAborted {
			c.isTransferAborted = false

			return nil, errNoTrasferConnection
		}

		c.writeMessage(StatusActionNotTaken, errNoTrasferConnection.Error())

		return nil, errNoTrasferConnection
	}

	if c.server.settings.TLSRequired == MandatoryEncryption && !c.transferTLS {
		c.writeMessage(StatusServiceNotAvailable, errTLSRequired.Error())

		return nil, errTLSRequired
	}

	conn, err := c.transfer.Open()
	if err != nil {
		c.logger.Warn(
			"Unable to open transfer",
			"error", err)

		c.writeMessage(StatusCannotOpenDataConnection, err.Error())

		return nil, err
	}

	c.isTransferOpen = true
	c.transfer.SetInfo(info)

	c.writeMessage(StatusFileStatusOK, "Using transfer connection")

	if c.debug {
		c.logger.Debug(
			"Transfer connection opened",
			"remoteAddr", conn.RemoteAddr().String(),
			"localAddr", conn.LocalAddr().String())
	}

	return conn, err
}

func (c *clientHandler) TransferClose(err error) {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	errClose := c.closeTransfer()
	if errClose != nil {
		c.logger.Warn(
			"Problem closing transfer connection",
			"err", err,
		)
	}

	// if the transfer was aborted we don't have to send a response
	if c.isTransferAborted {
		c.isTransferAborted = false

		return
	}

	switch {
	case err == nil && errClose == nil:
		c.writeMessage(StatusClosingDataConn, "Closing transfer connection")
	case errClose != nil:
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Issue during transfer close: %v", errClose))
	case err != nil:
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Issue during transfer: %v", err))
	}
}

func parseLine(line string) (string, string) {
	params := strings.SplitN(strings.Trim(line, "\r\n"), " ", 2)
	if len(params) == 1 {
		return params[0], ""
	}

	return params[0], params[1]
}

func (c *clientHandler) multilineAnswer(code int, message string) func() {
	c.writeLine(fmt.Sprintf("%d-%s", code, message))

	return func() {
		c.writeLine(fmt.Sprintf("%d End", code))
	}
}

func getMessageLines(message string) []string {
	lines := make([]string, 0, 1)
	sc := bufio.NewScanner(strings.NewReader(message))

	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if len(lines) == 0 {
		lines = append(lines, "")
	}

	return lines
}
