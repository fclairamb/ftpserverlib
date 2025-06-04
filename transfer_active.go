package ftpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (c *clientHandler) handlePORT(param string) error {
	command := c.GetLastCommand()

	if c.server.settings.DisableActiveMode {
		c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("%v command is disabled", command))

		return nil
	}

	var err error
	var raddr *net.TCPAddr

	if command == "EPRT" {
		raddr, err = parseEPRTAddr(param)
	} else { // PORT
		raddr, err = parsePORTAddr(param)
	}

	if err != nil {
		c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("Problem parsing %s: %v", param, err))

		return nil
	}

	err = c.checkDataConnectionRequirement(raddr.IP, DataChannelActive)
	if err != nil {
		// we don't want to expose the full error to the client, we just log it
		c.logger.Warn("Could not validate active data connection requirement", "err", err)
		c.writeMessage(StatusSyntaxErrorParameters, "Your request does not meet "+
			"the configured security requirements")

		return nil
	}

	var tlsConfig *tls.Config

	if c.HasTLSForTransfers() || c.server.settings.TLSRequired == ImplicitEncryption {
		tlsConfig, err = c.server.driver.GetTLSConfig()
		if err != nil {
			c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("Cannot get a TLS config for active connection: %v", err))

			return nil
		}
	}

	c.transferMu.Lock()

	c.transfer = &activeTransferHandler{
		raddr:     raddr,
		settings:  c.server.settings,
		tlsConfig: tlsConfig,
	}

	c.transferMu.Unlock()
	c.setLastDataChannel(DataChannelActive)

	c.writeMessage(StatusOK, command+" command successful")

	return nil
}

// activeTransferHandler implements the transferHandler interface
var _ transferHandler = (*activeTransferHandler)(nil)

// Active connection
type activeTransferHandler struct {
	raddr     *net.TCPAddr // Remote address of the client
	conn      net.Conn     // Connection used to connect to him
	settings  *Settings    // Settings
	tlsConfig *tls.Config  // not nil if the active connection requires TLS
	info      string       // transfer info
}

func (a *activeTransferHandler) GetInfo() string {
	return a.info
}

func (a *activeTransferHandler) SetInfo(info string) {
	a.info = info
}

func (a *activeTransferHandler) Open() (net.Conn, error) {
	timeout := time.Duration(time.Second.Nanoseconds() * int64(a.settings.ConnectionTimeout))
	dialer := &net.Dialer{Timeout: timeout}

	if !a.settings.ActiveTransferPortNon20 {
		dialer.LocalAddr, _ = net.ResolveTCPAddr("tcp", ":20")
		dialer.Control = Control
	}

	conn, err := dialer.Dial("tcp", a.raddr.String())
	if err != nil {
		return nil, newNetworkError("could not establish active connection", err)
	}

	if a.tlsConfig != nil {
		conn = tls.Server(conn, a.tlsConfig)
	}

	// keep connection as it will be closed by Close()
	a.conn = conn

	return a.conn, nil
}

// Close closes only if connection is established
func (a *activeTransferHandler) Close() error {
	if a.conn != nil {
		if err := a.conn.Close(); err != nil {
			return newNetworkError("could not close active connection", err)
		}
	}

	return nil
}

var remoteAddrRegex = regexp.MustCompile(`^([0-9]{1,3},){5}[0-9]{1,3}$`)

// ErrRemoteAddrFormat is returned when the remote address has a bad format
var ErrRemoteAddrFormat = errors.New("remote address has a bad format")

// parsePORTAddr parses remote address of the client from param. This address
// is used for establishing a connection with the client.
//
// Param Format: 192,168,150,80,14,178
// Host: 192.168.150.80
// Port: (14 * 256) + 148
func parsePORTAddr(param string) (*net.TCPAddr, error) {
	if !remoteAddrRegex.MatchString(param) {
		return nil, fmt.Errorf("could not parse %s: %w", param, ErrRemoteAddrFormat)
	}

	params := strings.Split(param, ",")

	ipParts := strings.Join(params[0:4], ".")

	portByte1, err := strconv.Atoi(params[4])
	if err != nil {
		return nil, ErrRemoteAddrFormat
	}

	portByte2, err := strconv.Atoi(params[5])
	if err != nil {
		return nil, ErrRemoteAddrFormat
	}

	port := portByte1<<8 + portByte2

	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", ipParts, port))
	if err != nil {
		err = newNetworkError("could not resolve "+param, err)
	}

	return addr, err
}

// Parse EPRT parameter. Full EPRT command format:
// - IPv4 : "EPRT |1|h1.h2.h3.h4|port|\r\n"
// - IPv6 : "EPRT |2|h1::h2:h3:h4:h5|port|\r\n"
func parseEPRTAddr(param string) (*net.TCPAddr, error) {
	var addr *net.TCPAddr
	var err error

	params := strings.Split(param, "|")
	if len(params) != 5 {
		return nil, ErrRemoteAddrFormat
	}

	netProtocol := params[1]
	remoteIP := params[2]
	remotePort := params[3]

	// check port is valid
	var portI int
	if portI, err = strconv.Atoi(remotePort); err != nil || portI <= 0 || portI > 65535 {
		return nil, ErrRemoteAddrFormat
	}

	var ipAddress net.IP

	switch netProtocol {
	case "1", "2":
		// use protocol 1 means IPv4. 2 means IPv6
		// net.ParseIP for validate IP
		if ipAddress = net.ParseIP(remoteIP); ipAddress == nil {
			return nil, ErrRemoteAddrFormat
		}
	default:
		// wrong network protocol
		return nil, ErrRemoteAddrFormat
	}

	addr, err = net.ResolveTCPAddr("tcp", net.JoinHostPort(ipAddress.String(), strconv.Itoa(portI)))
	if err != nil {
		err = newNetworkError(fmt.Sprintf("could not resolve addr %v:%v", ipAddress, portI), err)
	}

	return addr, err
}
