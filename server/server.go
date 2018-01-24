// Package server provides all the tools to build your own FTP server: The core library and the driver.
package server

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// logKeyAction is the machine-readable part of the log.
	logKeyAction = "action"

	// keepAlive is the timeout period used for all TCP sessions.
	keepAlive = time.Second * 30

	// dialTimeout is the timeout used for setting up active transfers.
	dialTimeout = time.Second * 30
)

// CommandDescription defines which function should be used and if it should be open to anyone or only logged in users
type CommandDescription struct {
	Open bool                       // Open to clients without auth
	Fn   func(*clientHandler) error // Function to handle it
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
	"FEAT": {Fn: (*clientHandler).handleFEAT, Open: true},
	"SYST": {Fn: (*clientHandler).handleSYST, Open: true},
	"NOOP": {Fn: (*clientHandler).handleNOOP, Open: true},
	"OPTS": {Fn: (*clientHandler).handleOPTS, Open: true},

	// File access
	"SIZE": {Fn: (*clientHandler).handleSIZE},
	"STAT": {Fn: (*clientHandler).handleSTAT},
	"MDTM": {Fn: (*clientHandler).handleMDTM},
	"RETR": {Fn: (*clientHandler).handleRETR},
	"STOR": {Fn: (*clientHandler).handleSTOR},
	"APPE": {Fn: (*clientHandler).handleAPPE},
	"DELE": {Fn: (*clientHandler).handleDELE},
	"RNFR": {Fn: (*clientHandler).handleRNFR},
	"RNTO": {Fn: (*clientHandler).handleRNTO},
	"ALLO": {Fn: (*clientHandler).handleALLO},
	"REST": {Fn: (*clientHandler).handleREST},
	"SITE": {Fn: (*clientHandler).handleSITE},

	// Directory handling
	"CWD":  {Fn: (*clientHandler).handleCWD},
	"PWD":  {Fn: (*clientHandler).handlePWD},
	"CDUP": {Fn: (*clientHandler).handleCDUP},
	"NLST": {Fn: (*clientHandler).handleLIST},
	"LIST": {Fn: (*clientHandler).handleLIST},
	"MLSD": {Fn: (*clientHandler).handleMLSD},
	"MLST": {Fn: (*clientHandler).handleMLST},
	"MKD":  {Fn: (*clientHandler).handleMKD},
	"RMD":  {Fn: (*clientHandler).handleRMD},

	// Connection handling
	"TYPE": {Fn: (*clientHandler).handleTYPE},
	"PASV": {Fn: (*clientHandler).handlePASV},
	"EPSV": {Fn: (*clientHandler).handlePASV},
	"PORT": {Fn: (*clientHandler).handlePORT},
	"QUIT": {Fn: (*clientHandler).handleQUIT, Open: true},
}

// FtpServer is where everything is stored
// We want to keep it as simple as possible
type FtpServer struct {
	*logrus.Entry              // Logrus Logger
	settings      *Settings    // General settings
	listener      net.Listener // listener used to accept connections on
	clientCounter uint32       // Clients counter
	driver        MainDriver   // Driver to handle the client authentication and the file access driver selection
	done          chan struct{}
	mtx           sync.Mutex
	clientMtx     sync.Mutex
	wg            sync.WaitGroup
	clients       map[uint32]*clientHandler
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

	server.settings = s

	return nil
}

// Listen starts the listening
// It's not a blocking call
func (server *FtpServer) Listen() error {
	err := server.loadSettings()
	if err != nil {
		return fmt.Errorf("could not load settings: %v", err)
	}

	if server.settings.Listener != nil {
		server.listener = server.settings.Listener
	} else {
		if server.listener, err = net.Listen("tcp", server.settings.ListenAddr); err != nil {
			server.Error("Listen failed ", err)
			return err
		}
	}

	server.WithFields(logrus.Fields{logKeyAction: "ftp.listening", "address": server.listener.Addr()}).Info("Listening...")

	return nil
}

// Stopped returns true of the server has been stopped
func (server *FtpServer) Stopped() bool {
	select {
	case <-server.done:
		return true
	default:
		return false
	}
}

// Serve accepts and process any new client coming
func (server *FtpServer) Serve() {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			if server.Stopped() {
				return
			}
			server.Error("Accept error ", err)
			continue
		}

		// Enable TCP KeepAlives to ensure we don't block forever when waiting for commands.
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			server.Errorf("Unexpected connection type %T", conn)
			conn.Close() // nolint: errcheck
			continue
		}

		if err := tcp.SetKeepAlive(true); err != nil {
			server.WithField(logKeyAction, "ftp.keep-alive").Error("Failed to enable keepalive ", err)
			conn.Close() // nolint: errcheck
			continue
		}

		if err := tcp.SetKeepAlivePeriod(keepAlive); err != nil {
			server.WithField(logKeyAction, "ftp.keep-alive-period").Error("Failed to set keepalive period ", err)
			conn.Close() // nolint: errcheck
			continue
		}

		server.clientArrival(conn)
	}
}

// ListenAndServe simply chains the Listen and Serve method calls
func (server *FtpServer) ListenAndServe() error {
	if err := server.Listen(); err != nil {
		return err
	}

	server.WithField(logKeyAction, "ftp.starting").Info("Starting...")

	server.Serve()

	// Note: At this precise time, the clients are still connected. We are just not accepting clients anymore.

	return nil
}

// NewFtpServer creates a new FtpServer instance
func NewFtpServer(driver MainDriver) *FtpServer {
	return &FtpServer{
		done:    make(chan struct{}),
		clients: make(map[uint32]*clientHandler),
		driver:  driver,
		Entry:   logrus.NewEntry(logrus.StandardLogger()),
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
func (server *FtpServer) Stop() {
	server.mtx.Lock()
	defer server.mtx.Unlock()

	select {
	case <-server.done:
		// Already stopped, no action needed
	default:
		close(server.done)
		if server.listener != nil {
			server.listener.Close() // nolint: errcheck
		}
		server.abortClients()
		server.wg.Wait()
	}
}

func (server *FtpServer) abortClients() {
	server.clientMtx.Lock()
	defer server.clientMtx.Unlock()

	for _, c := range server.clients {
		c.Close()
	}
}

// When a client connects, the server could refuse the connection
func (server *FtpServer) clientArrival(conn net.Conn) {
	server.clientMtx.Lock()
	defer server.clientMtx.Unlock()

	server.clientCounter++
	server.wg.Add(1)
	server.clients[server.clientCounter] = server.newClientHandler(conn, server.clientCounter, server.Entry)
}

// clientDeparture is called when a client is remove.
func (server *FtpServer) clientDeparture(c *clientHandler) {
	server.clientMtx.Lock()
	defer server.clientMtx.Unlock()

	delete(server.clients, c.id)
	server.wg.Done()
}
