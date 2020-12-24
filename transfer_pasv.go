// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/fclairamb/ftpserverlib/log"
)

// Active/Passive transfer connection handler
type transferHandler interface {
	// Get the connection to transfer data on
	Open() (net.Conn, error)

	// Close the connection (and any associated resource)
	Close() error

	// Set info about the transfer to return in STAT response
	SetInfo(string)
	// Info about the transfer to return in STAT response
	GetInfo() string
}

// Passive connection
type passiveTransferHandler struct {
	listener    net.Listener     // TCP or SSL Listener
	tcpListener *net.TCPListener // TCP Listener (only keeping it to define a deadline during the accept)
	Port        int              // TCP Port we are listening on
	connection  net.Conn         // TCP Connection established
	settings    *Settings        // Settings
	info        string           // transfer info
	logger      log.Logger       // Logger
}

func (c *clientHandler) getCurrentIP() ([]string, error) {
	// Provide our external IP address so the ftp client can connect back to us
	ip := c.server.settings.PublicHost

	// If we don't have an IP address, we can take the one that was used for the current connection
	if ip == "" {
		// Defer to the user-provided resolver.
		if c.server.settings.PublicIPResolver != nil {
			var err error
			ip, err = c.server.settings.PublicIPResolver(c)

			if err != nil {
				return nil, fmt.Errorf("couldn't fetch public IP: %w", err)
			}
		} else {
			ip = strings.Split(c.conn.LocalAddr().String(), ":")[0]
		}
	}

	return strings.Split(ip, "."), nil
}

// ErrNoAvailableListeningPort is returned when no port could be found to accept incoming connection
var ErrNoAvailableListeningPort = errors.New("could not find any port to listen to")

func (c *clientHandler) findListenerWithinPortRange(portRange *PortRange) (*net.TCPListener, error) {
	nbAttempts := portRange.End - portRange.Start

	// Making sure we trying a reasonable amount of ports before giving up
	if nbAttempts < 10 {
		nbAttempts = 10
	} else if nbAttempts > 1000 {
		nbAttempts = 1000
	}

	for i := 0; i < nbAttempts; i++ {
		// nolint: gosec
		port := portRange.Start + rand.Intn(portRange.End-portRange.Start+1)
		laddr, errResolve := net.ResolveTCPAddr("tcp", fmt.Sprintf("0.0.0.0:%d", port))

		if errResolve != nil {
			c.logger.Error("Problem resolving local port", "err", errResolve, "port", port)

			return nil, fmt.Errorf("could not resolve port %d: %w", port, errResolve)
		}

		tcpListener, errListen := net.ListenTCP("tcp", laddr)
		if errListen == nil {
			return tcpListener, errListen
		}
	}

	c.logger.Warn(
		"Could not find any free port",
		"nbAttempts", nbAttempts,
		"portRangeStart", portRange.Start,
		"portRAngeEnd", portRange.End,
	)

	return nil, ErrNoAvailableListeningPort
}

func (c *clientHandler) handlePASV(param string) error {
	command := c.GetLastCommand()
	addr, _ := net.ResolveTCPAddr("tcp", ":0")

	var tcpListener *net.TCPListener
	var err error

	portRange := c.server.settings.PassiveTransferPortRange

	if portRange != nil {
		tcpListener, err = c.findListenerWithinPortRange(portRange)
	} else {
		tcpListener, err = net.ListenTCP("tcp", addr)
	}

	if err != nil {
		c.logger.Error("Could not listen for passive connection", "err", err)
		c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Could not listen for passive connection: %v", err))

		return nil
	}

	// The listener will either be plain TCP or TLS
	var listener net.Listener

	if c.HasTLSForTransfers() || c.server.settings.TLSRequired == ImplicitEncryption {
		if tlsConfig, err := c.server.driver.GetTLSConfig(); err == nil {
			listener = tls.NewListener(tcpListener, tlsConfig)
		} else {
			c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Cannot get a TLS config: %v", err))

			return nil
		}
	} else {
		listener = tcpListener
	}

	p := &passiveTransferHandler{
		tcpListener: tcpListener,
		listener:    listener,
		Port:        tcpListener.Addr().(*net.TCPAddr).Port,
		settings:    c.server.settings,
		logger:      c.logger,
	}

	// We should rewrite this part
	if command == "PASV" {
		p1 := p.Port / 256
		p2 := p.Port - (p1 * 256)
		quads, err2 := c.getCurrentIP()

		if err2 != nil {
			c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Could not listen for passive connection: %v", err2))

			return nil
		}

		c.writeMessage(
			StatusEnteringPASV,
			fmt.Sprintf("Entering Passive Mode (%s,%s,%s,%s,%d,%d)", quads[0], quads[1], quads[2], quads[3], p1, p2))
	} else {
		c.writeMessage(StatusEnteringEPSV, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", p.Port))
	}

	c.transferMu.Lock()
	c.transfer = p
	c.transferMu.Unlock()

	return nil
}

func (p *passiveTransferHandler) ConnectionWait(wait time.Duration) (net.Conn, error) {
	if p.connection == nil {
		var err error
		if err = p.tcpListener.SetDeadline(time.Now().Add(wait)); err != nil {
			return nil, fmt.Errorf("failed to set deadline: %w", err)
		}

		p.connection, err = p.listener.Accept()

		if err != nil {
			return nil, err
		}
	}

	return p.connection, nil
}

func (p *passiveTransferHandler) GetInfo() string {
	return p.info
}

func (p *passiveTransferHandler) SetInfo(info string) {
	p.info = info
}

func (p *passiveTransferHandler) Open() (net.Conn, error) {
	timeout := time.Duration(time.Second.Nanoseconds() * int64(p.settings.ConnectionTimeout))

	return p.ConnectionWait(timeout)
}

// Closing only the client connection is not supported at that time
func (p *passiveTransferHandler) Close() error {
	if p.tcpListener != nil {
		if err := p.tcpListener.Close(); err != nil {
			p.logger.Warn(
				"Problem closing passive listener",
				"err", err,
			)
		}
	}

	if p.connection != nil {
		if err := p.connection.Close(); err != nil {
			p.logger.Warn(
				"Problem closing passive connection",
				"err", err,
			)
		}
	}

	return nil
}
