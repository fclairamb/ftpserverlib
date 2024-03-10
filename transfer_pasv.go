package ftpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	log "github.com/fclairamb/go-log"
)

// Active/Passive transfer connection handler
type transferHandler interface {
	// Get the connection to transfer data on
	Open() (net.Conn, error)

	// Close the connection (and any associated resource)
	Close() error

	// Set info about the transfer to return in STAT response
	SetInfo(info string)
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
	// data connection requirement checker
	checkDataConn func(dataConnIP net.IP, channelType DataChannel) error
}

type ipValidationError struct {
	error string
}

func (e *ipValidationError) Error() string {
	return e.error
}

func (c *clientHandler) getCurrentIP() ([]string, error) {
	// Provide our external IP address so the ftp client can connect back to us
	ipParts := c.server.settings.PublicHost

	// If we don't have an IP address, we can take the one that was used for the current connection
	if ipParts == "" {
		// Defer to the user-provided resolver.
		if c.server.settings.PublicIPResolver != nil {
			var err error
			ipParts, err = c.server.settings.PublicIPResolver(c)

			if err != nil {
				return nil, fmt.Errorf("couldn't fetch public IP: %w", err)
			}
		} else {
			ipParts = strings.Split(c.conn.LocalAddr().String(), ":")[0]
		}
	}

	quads := strings.Split(ipParts, ".")
	if len(quads) != 4 {
		c.logger.Warn("Invalid passive IP", "IP", ipParts)

		return nil, &ipValidationError{error: fmt.Sprintf("invalid passive IP %#v", ipParts)}
	}

	return quads, nil
}

// ErrNoAvailableListeningPort is returned when no port could be found to accept incoming connection
var ErrNoAvailableListeningPort = errors.New("could not find any port to listen to")

const (
	portSearchMinAttempts = 10
	portSearchMaxAttempts = 1000
)

func (c *clientHandler) findListenerWithinPortRange(portRange *PortRange) (*net.TCPListener, error) {
	nbAttempts := portRange.End - portRange.Start

	// Making sure we trying a reasonable amount of ports before giving up
	if nbAttempts < portSearchMinAttempts {
		nbAttempts = portSearchMinAttempts
	} else if nbAttempts > portSearchMaxAttempts {
		nbAttempts = portSearchMaxAttempts
	}

	for i := 0; i < nbAttempts; i++ {
		//nolint: gosec
		port := portRange.Start + rand.Intn(portRange.End-portRange.Start+1)
		laddr, errResolve := net.ResolveTCPAddr("tcp", fmt.Sprintf("0.0.0.0:%d", port))

		if errResolve != nil {
			c.logger.Error("Problem resolving local port", "err", errResolve, "port", port)

			return nil, newNetworkError(fmt.Sprintf("could not resolve port %d", port), errResolve)
		}

		tcpListener, errListen := net.ListenTCP("tcp", laddr)
		if errListen == nil {
			return tcpListener, nil
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

func (c *clientHandler) handlePASV(_ string) error {
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
	listener = tcpListener

	if wrapper, ok := c.server.driver.(MainDriverExtensionPassiveWrapper); ok {
		listener, err = wrapper.WrapPassiveListener(listener)
		if err != nil {
			c.logger.Error("Could not wrap passive connection", "err", err)
			c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Could not listen for passive connection: %v", err))

			return nil
		}
	}

	if c.HasTLSForTransfers() || c.server.settings.TLSRequired == ImplicitEncryption {
		if tlsConfig, err := c.server.driver.GetTLSConfig(); err == nil {
			listener = tls.NewListener(listener, tlsConfig)
		} else {
			c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Cannot get a TLS config: %v", err))

			return nil
		}
	}

	transferHandler := &passiveTransferHandler{ //nolint:forcetypeassert
		tcpListener:   tcpListener,
		listener:      listener,
		Port:          tcpListener.Addr().(*net.TCPAddr).Port,
		settings:      c.server.settings,
		logger:        c.logger,
		checkDataConn: c.checkDataConnectionRequirement,
	}

	// We should rewrite this part
	if command == "PASV" {
		if c.handlePassivePASV(transferHandler) {
			return nil
		}
	} else {
		c.writeMessage(StatusEnteringEPSV, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", transferHandler.Port))
	}

	c.transferMu.Lock()
	if c.transfer != nil {
		c.transfer.Close() //nolint:errcheck,gosec
	}

	c.transfer = transferHandler
	c.transferMu.Unlock()
	c.setLastDataChannel(DataChannelPassive)

	return nil
}

func (c *clientHandler) handlePassivePASV(transferHandler *passiveTransferHandler) bool {
	portByte1 := transferHandler.Port / 256
	portByte2 := transferHandler.Port - (portByte1 * 256)
	quads, err2 := c.getCurrentIP()

	if err2 != nil {
		c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Could not listen for passive connection: %v", err2))

		return true
	}

	c.writeMessage(
		StatusEnteringPASV,
		fmt.Sprintf(
			"Entering Passive Mode (%s,%s,%s,%s,%d,%d)",
			quads[0], quads[1], quads[2], quads[3],
			portByte1, portByte2,
		),
	)

	return false
}

func (p *passiveTransferHandler) ConnectionWait(wait time.Duration) (net.Conn, error) {
	if p.connection == nil {
		var err error
		if err = p.tcpListener.SetDeadline(time.Now().Add(wait)); err != nil {
			return nil, fmt.Errorf("failed to set deadline: %w", err)
		}

		p.connection, err = p.listener.Accept()

		if err != nil {
			return nil, fmt.Errorf("failed to accept passive transfer connection: %w", err)
		}

		ipAddress, err := getIPFromRemoteAddr(p.connection.RemoteAddr())
		if err != nil {
			p.logger.Warn("Could get remote passive IP address", "err", err)

			return nil, err
		}

		if err := p.checkDataConn(ipAddress, DataChannelPassive); err != nil {
			// we don't want to expose the full error to the client, we just log it
			p.logger.Warn("Could not validate passive data connection requirement", "err", err)

			return nil, &ipValidationError{error: "data connection security requirements not met"}
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
			p.logger.Warn("Problem closing passive listener", "err", err)
		}
	}

	if p.connection != nil {
		if err := p.connection.Close(); err != nil {
			p.logger.Warn(
				"Problem closing passive connection", "err", err)
		}
	}

	return nil
}
