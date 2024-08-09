// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	log "github.com/fclairamb/go-log"
	lognoop "github.com/fclairamb/go-log/noop"
)

// ErrNotListening is returned when we are performing an action that is only valid while listening
var ErrNotListening = errors.New("we aren't listening")

// CommandDescription defines which function should be used and if it should be open to anyone or only logged in users
type CommandDescription struct {
	Open            bool                               // Open to clients without auth
	TransferRelated bool                               // This is a command that can open a transfer connection
	SpecialAction   bool                               // Command to handle even if there is a transfer in progress
	Fn              func(*clientHandler, string) error // Function to handle it
}

// This is shared between FtpServer instances as there's no point in making the FTP commands behave differently
// between them.
var commandsMap = map[string]*CommandDescription{ //nolint:gochecknoglobals
	// Authentication
	"USER": {Fn: (*clientHandler).handleUSER, Open: true},
	"PASS": {Fn: (*clientHandler).handlePASS, Open: true},
	"ACCT": {Fn: (*clientHandler).handleNotImplemented},
	"ADAT": {Fn: (*clientHandler).handleNotImplemented},

	// TLS handling
	"AUTH": {Fn: (*clientHandler).handleAUTH, Open: true},
	"PROT": {Fn: (*clientHandler).handlePROT, Open: true},
	"PBSZ": {Fn: (*clientHandler).handlePBSZ, Open: true},
	"CCC":  {Fn: (*clientHandler).handleNotImplemented},
	"CONF": {Fn: (*clientHandler).handleNotImplemented},
	"ENC":  {Fn: (*clientHandler).handleNotImplemented},
	"MIC":  {Fn: (*clientHandler).handleNotImplemented},

	// Misc
	"CLNT": {Fn: (*clientHandler).handleCLNT, Open: true},
	"FEAT": {Fn: (*clientHandler).handleFEAT, Open: true},
	"SYST": {Fn: (*clientHandler).handleSYST, Open: true},
	"NOOP": {Fn: (*clientHandler).handleNOOP, Open: true},
	"OPTS": {Fn: (*clientHandler).handleOPTS, Open: true},
	"QUIT": {Fn: (*clientHandler).handleQUIT, Open: true, SpecialAction: true},
	"AVBL": {Fn: (*clientHandler).handleAVBL},
	"ABOR": {Fn: (*clientHandler).handleABOR, SpecialAction: true},
	"CSID": {Fn: (*clientHandler).handleNotImplemented},
	"HELP": {Fn: (*clientHandler).handleNotImplemented},
	"HOST": {Fn: (*clientHandler).handleNotImplemented},
	"LANG": {Fn: (*clientHandler).handleNotImplemented},
	"XRSQ": {Fn: (*clientHandler).handleNotImplemented},
	"XSEM": {Fn: (*clientHandler).handleNotImplemented},
	"XSEN": {Fn: (*clientHandler).handleNotImplemented},

	// File access
	"SIZE":    {Fn: (*clientHandler).handleSIZE},
	"DSIZ":    {Fn: (*clientHandler).handleNotImplemented},
	"STAT":    {Fn: (*clientHandler).handleSTAT, SpecialAction: true},
	"MDTM":    {Fn: (*clientHandler).handleMDTM},
	"MFMT":    {Fn: (*clientHandler).handleMFMT},
	"MFF":     {Fn: (*clientHandler).handleNotImplemented},
	"MFCT":    {Fn: (*clientHandler).handleNotImplemented},
	"RETR":    {Fn: (*clientHandler).handleRETR, TransferRelated: true},
	"STOR":    {Fn: (*clientHandler).handleSTOR, TransferRelated: true},
	"STOU":    {Fn: (*clientHandler).handleNotImplemented},
	"STRU":    {Fn: (*clientHandler).handleNotImplemented},
	"APPE":    {Fn: (*clientHandler).handleAPPE, TransferRelated: true},
	"DELE":    {Fn: (*clientHandler).handleDELE},
	"RNFR":    {Fn: (*clientHandler).handleRNFR},
	"RNTO":    {Fn: (*clientHandler).handleRNTO},
	"ALLO":    {Fn: (*clientHandler).handleALLO},
	"REST":    {Fn: (*clientHandler).handleREST},
	"SITE":    {Fn: (*clientHandler).handleSITE},
	"HASH":    {Fn: (*clientHandler).handleHASH},
	"XCRC":    {Fn: (*clientHandler).handleCRC32},
	"MD5":     {Fn: (*clientHandler).handleMD5},
	"XMD5":    {Fn: (*clientHandler).handleMD5},
	"XSHA":    {Fn: (*clientHandler).handleSHA1},
	"XSHA1":   {Fn: (*clientHandler).handleSHA1},
	"XSHA256": {Fn: (*clientHandler).handleSHA256},
	"XSHA512": {Fn: (*clientHandler).handleSHA512},
	"COMB":    {Fn: (*clientHandler).handleCOMB},
	"THMB":    {Fn: (*clientHandler).handleNotImplemented},
	"XRCP":    {Fn: (*clientHandler).handleNotImplemented},

	// Directory handling
	"CWD":  {Fn: (*clientHandler).handleCWD},
	"PWD":  {Fn: (*clientHandler).handlePWD},
	"XCWD": {Fn: (*clientHandler).handleCWD},
	"XPWD": {Fn: (*clientHandler).handlePWD},
	"CDUP": {Fn: (*clientHandler).handleCDUP},
	"NLST": {Fn: (*clientHandler).handleNLST, TransferRelated: true},
	"LIST": {Fn: (*clientHandler).handleLIST, TransferRelated: true},
	"MLSD": {Fn: (*clientHandler).handleMLSD, TransferRelated: true},
	"MLST": {Fn: (*clientHandler).handleMLST},
	"MKD":  {Fn: (*clientHandler).handleMKD},
	"RMD":  {Fn: (*clientHandler).handleRMD},
	"RMDA": {Fn: (*clientHandler).handleNotImplemented},
	"XMKD": {Fn: (*clientHandler).handleMKD},
	"XRMD": {Fn: (*clientHandler).handleRMD},
	"SMNT": {Fn: (*clientHandler).handleNotImplemented},
	"XCUP": {Fn: (*clientHandler).handleNotImplemented},

	// Connection handling
	"TYPE": {Fn: (*clientHandler).handleTYPE},
	"MODE": {Fn: (*clientHandler).handleMODE},
	"PASV": {Fn: (*clientHandler).handlePASV},
	"EPSV": {Fn: (*clientHandler).handlePASV},
	"LPSV": {Fn: (*clientHandler).handleNotImplemented},
	"SPSV": {Fn: (*clientHandler).handleNotImplemented},
	"PORT": {Fn: (*clientHandler).handlePORT},
	"LRPT": {Fn: (*clientHandler).handleNotImplemented},
	"EPRT": {Fn: (*clientHandler).handlePORT},
	"REIN": {Fn: (*clientHandler).handleNotImplemented},
}

var specialAttentionCommands = []string{"ABOR", "STAT", "QUIT"} //nolint:gochecknoglobals

// FtpServer is where everything is stored
// We want to keep it as simple as possible
type FtpServer struct {
	Logger        log.Logger   // fclairamb/go-log generic logger
	settings      *Settings    // General settings
	listener      net.Listener // listener used to receive files
	clientCounter uint32       // Clients counter
	driver        MainDriver   // Driver to handle the client authentication and the file access driver selection
}

func (server *FtpServer) loadSettings() error {
	settings, err := server.driver.GetSettings()

	if err != nil || settings == nil {
		return newDriverError("couldn't load settings", err)
	}

	if settings.PublicHost != "" {
		settings.PublicHost, err = parseIPv4(settings.PublicHost)
		if err != nil {
			return err
		}
	}

	if settings.Listener == nil && settings.ListenAddr == "" {
		settings.ListenAddr = "0.0.0.0:2121"
	}

	// florent(2018-01-14): #58: IDLE timeout: Default idle timeout will be set at 900 seconds
	if settings.IdleTimeout == 0 {
		settings.IdleTimeout = 900
	}

	if settings.ConnectionTimeout == 0 {
		settings.ConnectionTimeout = 30
	}

	if settings.Banner == "" {
		settings.Banner = "ftpserver - golang FTP server"
	}

	server.settings = settings

	return nil
}

func parseIPv4(publicHost string) (string, error) {
	parsedIP := net.ParseIP(publicHost)
	if parsedIP == nil {
		return "", &ipValidationError{error: fmt.Sprintf("invalid passive IP %#v", publicHost)}
	}

	parsedIP = parsedIP.To4()
	if parsedIP == nil {
		return "", &ipValidationError{error: fmt.Sprintf("invalid IPv4 passive IP %#v", publicHost)}
	}

	return parsedIP.String(), nil
}

// Listen starts the listening
// It's not a blocking call
func (server *FtpServer) Listen() error {
	err := server.loadSettings()
	if err != nil {
		return fmt.Errorf("could not load settings: %w", err)
	}

	// The driver can provide its own listener implementation
	if server.settings.Listener != nil {
		server.listener = server.settings.Listener
	} else {
		// Otherwise, it's what we currently use
		server.listener, err = server.createListener()
		if err != nil {
			return fmt.Errorf("could not create listener: %w", err)
		}
	}

	server.Logger.Info("Listening...", "address", server.listener.Addr())

	return nil
}

func (server *FtpServer) createListener() (net.Listener, error) {
	listener, err := net.Listen("tcp", server.settings.ListenAddr)
	if err != nil {
		server.Logger.Error("cannot listen on main port", "err", err, "listenAddr", server.settings.ListenAddr)

		return nil, newNetworkError("cannot listen on main port", err)
	}

	if server.settings.TLSRequired == ImplicitEncryption {
		// implicit TLS
		var tlsConfig *tls.Config

		tlsConfig, err = server.driver.GetTLSConfig()
		if err != nil || tlsConfig == nil {
			server.Logger.Error("Cannot get tls config", "err", err)

			return nil, newDriverError("cannot get tls config", err)
		}

		listener = tls.NewListener(listener, tlsConfig)
	}

	return listener, nil
}

func temporaryError(err net.Error) bool {
	if syscallErrNo := new(syscall.Errno); errors.As(err, syscallErrNo) {
		if *syscallErrNo == syscall.ECONNABORTED || *syscallErrNo == syscall.ECONNRESET {
			return true
		}
	}

	return false
}

// Serve accepts and processes any new incoming client
func (server *FtpServer) Serve() error {
	var tempDelay time.Duration // how long to sleep on accept failure

	for {
		connection, err := server.listener.Accept()
		if err != nil {
			if ok, finalErr := server.handleAcceptError(err, &tempDelay); ok {
				return finalErr
			}

			continue
		}

		tempDelay = 0

		server.clientArrival(connection)
	}
}

// handleAcceptError handles the error that occurred when accepting a new connection
// It returns a boolean indicating if the error should stop the server and the error itself or none if it's a standard
// scenario (e.g. a closed listener)
func (server *FtpServer) handleAcceptError(err error, tempDelay *time.Duration) (bool, error) {
	server.Logger.Error("Serve error", "err", err)

	if errOp := (&net.OpError{}); errors.As(err, &errOp) {
		// This means we just closed the connection and it's OK
		if errOp.Err.Error() == "use of closed network connection" {
			server.listener = nil

			return true, nil
		}
	}

	// see https://github.com/golang/go/blob/4aa1efed4853ea067d665a952eee77c52faac774/src/net/http/server.go#L3046
	// & https://github.com/fclairamb/ftpserverlib/pull/352#pullrequestreview-1077459896
	// The temporaryError method should replace net.Error.Temporary() when the go team
	// will have provided us a better way to detect temporary errors.
	var ne net.Error
	if errors.As(err, &ne) && ne.Temporary() { //nolint:staticcheck
		if *tempDelay == 0 {
			*tempDelay = 5 * time.Millisecond
		} else {
			*tempDelay *= 2
		}

		if max := 1 * time.Second; *tempDelay > max {
			*tempDelay = max
		}

		server.Logger.Warn(
			"accept error", err,
			"retry delay", tempDelay)
		time.Sleep(*tempDelay)

		return false, nil
	}

	server.Logger.Error("Listener accept error", "err", err)

	return true, newNetworkError("listener accept error", err)
}

// ListenAndServe simply chains the Listen and Serve method calls
func (server *FtpServer) ListenAndServe() error {
	if err := server.Listen(); err != nil {
		return err
	}

	server.Logger.Info("Starting...")

	return server.Serve()
}

// NewFtpServer creates a new FtpServer instance
func NewFtpServer(driver MainDriver) *FtpServer {
	return &FtpServer{
		driver: driver,
		Logger: lognoop.NewNoOpLogger(),
	}
}

// Addr shows the listening address
func (server *FtpServer) Addr() string {
	if server.listener != nil {
		return server.listener.Addr().String()
	}

	return ""
}

// Stop closes the listener
func (server *FtpServer) Stop() error {
	if server.listener == nil {
		return ErrNotListening
	}

	if err := server.listener.Close(); err != nil {
		server.Logger.Warn(
			"Could not close listener",
			"err", err,
		)

		return newNetworkError("couln't close listener", err)
	}

	return nil
}

// When a client connects, the server could refuse the connection
func (server *FtpServer) clientArrival(conn net.Conn) {
	server.clientCounter++
	id := server.clientCounter

	c := server.newClientHandler(conn, id, server.settings.DefaultTransferType)
	go c.HandleCommands()

	c.logger.Debug("Client connected", "clientIp", conn.RemoteAddr())
}

// clientDeparture
func (server *FtpServer) clientDeparture(c *clientHandler) {
	c.logger.Debug("Client disconnected", "clientIp", c.conn.RemoteAddr())
}
