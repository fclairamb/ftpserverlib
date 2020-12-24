// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
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
		c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Problem parsing %s: %v", param, err))

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

	c.writeMessage(StatusOK, command+" command successful")

	return nil
}

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
	}
	// TODO(mgenov): support dialing with timeout
	// Issues:
	//	https://github.com/golang/go/issues/3097
	// 	https://github.com/golang/go/issues/4842
	conn, err := dialer.Dial("tcp", a.raddr.String())

	if err != nil {
		return nil, fmt.Errorf("could not establish active connection: %w", err)
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
		return a.conn.Close()
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
	if !remoteAddrRegex.Match([]byte(param)) {
		return nil, fmt.Errorf("could not parse %s: %w", param, ErrRemoteAddrFormat)
	}

	params := strings.Split(param, ",")

	ip := strings.Join(params[0:4], ".")

	p1, err := strconv.Atoi(params[4])
	if err != nil {
		return nil, err
	}

	p2, err := strconv.Atoi(params[5])

	if err != nil {
		return nil, err
	}

	port := p1<<8 + p2

	return net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", ip, port))
}

// Parse EPRT parameter. Full EPRT command format:
// - IPv4 : "EPRT |1|h1.h2.h3.h4|port|\r\n"
// - IPv6 : "EPRT |2|h1::h2:h3:h4:h5|port|\r\n"
func parseEPRTAddr(param string) (addr *net.TCPAddr, err error) {
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

	var ip net.IP

	switch netProtocol {
	case "1", "2":
		// use protocol 1 means IPv4. 2 means IPv6
		// net.ParseIP for validate IP
		if ip = net.ParseIP(remoteIP); ip == nil {
			return nil, ErrRemoteAddrFormat
		}
	default:
		// wrong network protocol
		return nil, ErrRemoteAddrFormat
	}

	return net.ResolveTCPAddr("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(portI)))
}
