package server

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

func (c *clientHandler) handlePORT() error {
	raddr, err := parseRemoteAddr(c.param)
	if err != nil {
		return c.writeMessage(500, fmt.Sprintf("Problem parsing PORT: %v", err))
	}

	c.setTransfer(&activeTransferHandler{raddr: raddr, nonStandardPort: c.daddy.settings.NonStandardActiveDataPort})

	return c.writeMessage(200, "PORT command successful")
}

// Active connection
type activeTransferHandler struct {
	raddr           *net.TCPAddr // Remote address of the client
	conn            net.Conn     // Connection used to connect to him
	nonStandardPort bool         // Allow to use an other port than the 20 one
}

func (a *activeTransferHandler) Open() (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	if !a.nonStandardPort {
		laddr, err := net.ResolveTCPAddr("tcp", ":20")
		if err != nil {
			return nil, fmt.Errorf("failed to resolve local address: %v", err)
		}
		d.LocalAddr = laddr
	}

	conn, err := d.Dial("tcp", a.raddr.String())
	if err != nil {
		return nil, fmt.Errorf("could not establish active connection due: %v", err)
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

// parseRemoteAddr parses remote address of the client from param. This address
// is used for establishing a connection with the client.
//
// Param Format: 192,168,150,80,14,178
// Host: 192.168.150.80
// Port: (14 * 256) + 148
func parseRemoteAddr(param string) (*net.TCPAddr, error) {
	//TODO(mgenov): ensure that format of the params is valid
	params := strings.Split(param, ",")
	if len(params) != 6 {
		return nil, errors.New("bad number of args")
	}
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
