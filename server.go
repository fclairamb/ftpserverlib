// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/fclairamb/ftpserverlib/log"
)

var (
	// ErrNotListening is returned when we are performing an action that is only valid while listening
	ErrNotListening = errors.New("we aren't listening")
)

// CommandDescription defines which function should be used and if it should be open to anyone or only logged in users
type CommandDescription struct {
	Open            bool                               // Open to clients without auth
	TransferRelated bool                               // This is a command that can open a transfer connection
	SpecialAction   bool                               // Command to handle even if there is a transfer in progress
	Fn              func(*clientHandler, string) error // Function to handle it
}

// This is shared between FtpServer instances as there's no point in making the FTP commands behave differently
// between them.
var commandsMap = map[string]*CommandDescription{
	// Authentication
	"USER": {Fn: (*clientHandler).handleUSER, Open: true},
	"PASS": {Fn: (*clientHandler).handlePASS, Open: true},

	// TLS handling
	"AUTH": {Fn: (*clientHandler).handleAUTH, Open: true},
	"PROT": {Fn: (*clientHandler).handlePROT, Open: true},
	"PBSZ": {Fn: (*clientHandler).handlePBSZ, Open: true},

	// Misc
	"CLNT": {Fn: (*clientHandler).handleCLNT, Open: true},
	"FEAT": {Fn: (*clientHandler).handleFEAT, Open: true},
	"SYST": {Fn: (*clientHandler).handleSYST, Open: true},
	"NOOP": {Fn: (*clientHandler).handleNOOP, Open: true},
	"OPTS": {Fn: (*clientHandler).handleOPTS, Open: true},
	"QUIT": {Fn: (*clientHandler).handleQUIT, Open: true, SpecialAction: true},
	"AVBL": {Fn: (*clientHandler).handleAVBL},
	"ABOR": {Fn: (*clientHandler).handleABOR, SpecialAction: true},

	// File access
	"SIZE":    {Fn: (*clientHandler).handleSIZE},
	"STAT":    {Fn: (*clientHandler).handleSTAT, SpecialAction: true},
	"MDTM":    {Fn: (*clientHandler).handleMDTM},
	"MFMT":    {Fn: (*clientHandler).handleMFMT},
	"RETR":    {Fn: (*clientHandler).handleRETR, TransferRelated: true},
	"STOR":    {Fn: (*clientHandler).handleSTOR, TransferRelated: true},
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
	"XMKD": {Fn: (*clientHandler).handleMKD},
	"XRMD": {Fn: (*clientHandler).handleRMD},

	// Connection handling
	"TYPE": {Fn: (*clientHandler).handleTYPE},
	"PASV": {Fn: (*clientHandler).handlePASV},
	"EPSV": {Fn: (*clientHandler).handlePASV},
	"PORT": {Fn: (*clientHandler).handlePORT},
	"EPRT": {Fn: (*clientHandler).handlePORT},
}

var (
	specialAttentionCommands = []string{"ABOR", "STAT", "QUIT"}
)

// FtpServer is where everything is stored
// We want to keep it as simple as possible
type FtpServer struct {
	Logger        log.Logger   // Go-Kit logger
	settings      *Settings    // General settings
	listener      net.Listener // listener used to receive files
	clientCounter uint32       // Clients counter
	driver        MainDriver   // Driver to handle the client authentication and the file access driver selection
}

func (server *FtpServer) loadSettings() error {
	s, err := server.driver.GetSettings()

	if err != nil {
		return err
	}

	if s.Listener == nil && s.ListenAddr == "" {
		s.ListenAddr = "0.0.0.0:2121"
	}

	// florent(2018-01-14): #58: IDLE timeout: Default idle timeout will be set at 900 seconds
	if s.IdleTimeout == 0 {
		s.IdleTimeout = 900
	}

	if s.ConnectionTimeout == 0 {
		s.ConnectionTimeout = 30
	}

	if s.Banner == "" {
		s.Banner = "ftpserver - golang FTP server"
	}

	server.settings = s

	return nil
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
		server.listener, err = net.Listen("tcp", server.settings.ListenAddr)

		if err != nil {
			server.Logger.Error("Cannot listen", "err", err)

			return err
		}
		if server.settings.TLSRequired == ImplicitEncryption {
			// implicit TLS
			var tlsConfig *tls.Config

			tlsConfig, err = server.driver.GetTLSConfig()
			if err != nil {
				server.Logger.Error("Cannot get tls config", "err", err)

				return err
			}
			server.listener = tls.NewListener(server.listener, tlsConfig)
		}
	}

	server.Logger.Info("Listening...", "address", server.listener.Addr())

	return err
}

// Serve accepts and processes any new incoming client
func (server *FtpServer) Serve() error {
	var tempDelay time.Duration // how long to sleep on accept failure

	for {
		connection, err := server.listener.Accept()

		if err != nil {
			if errOp, ok := err.(*net.OpError); ok {
				// This means we just closed the connection and it's OK
				if errOp.Err.Error() == "use of closed network connection" {
					server.listener = nil

					return nil
				}
			}

			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}

				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}

				server.Logger.Warn(
					"accept error", err,
					"retry delay", tempDelay)
				time.Sleep(tempDelay)

				continue
			}

			server.Logger.Error("Listener accept error", "err", err)

			return err
		}

		server.clientArrival(connection)
	}
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
		Logger: log.Nothing(),
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

	err := server.listener.Close()
	if err != nil {
		server.Logger.Warn(
			"Could not close listener",
			"err", err,
		)
	}

	return err
}

// When a client connects, the server could refuse the connection
func (server *FtpServer) clientArrival(conn net.Conn) {
	server.clientCounter++
	id := server.clientCounter

	c := server.newClientHandler(conn, id, server.settings.DefaultTransferType)
	go c.HandleCommands()

	c.logger.Info("Client connected", "clientIp", conn.RemoteAddr())
}

// clientDeparture
func (server *FtpServer) clientDeparture(c *clientHandler) {
	c.logger.Info("Client disconnected", "clientIp", c.conn.RemoteAddr())
}
