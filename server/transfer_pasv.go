package server

import (
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

// Active/Passive transfer connection handler
type transferHandler interface {
	// Get the connection to transfer data on
	Open() (net.Conn, error)

	// Close the connection (and any associated resource)
	Close() error
}

// Passive connection
type passiveTransferHandler struct {
	listener    net.Listener     // TCP or SSL Listener
	tcpListener *net.TCPListener // TCP Listener (only keeping it to define a deadline during the accept)
	Port        int              // TCP Port we are listening on
	connection  net.Conn         // TCP Connection established
}

func (c *clientHandler) pasvListener() (*net.TCPListener, error) {
	portRange := c.daddy.settings.DataPortRange
	if portRange != nil {
		for start := portRange.Start; start < portRange.End; start++ {
			port := portRange.Start + rand.Intn(portRange.End-portRange.Start)
			laddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("0.0.0.0:%v", port))
			if err != nil {
				continue
			}

			if l, err := net.ListenTCP("tcp", laddr); err == nil {
				return l, nil
			}
		}
		return nil, fmt.Errorf("no port found")
	}

	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		return nil, err
	}
	return net.ListenTCP("tcp", addr)
}

func (c *clientHandler) handlePASV() (err error) {
	tcpListener, err := c.pasvListener()
	if err != nil {
		// Just log so the client can continue
		c.logger.Error("Could not listen ", err)
		return nil
	}

	// The listener will either be plain TCP or TLS
	var listener net.Listener
	if c.transferTLS {
		tlsConfig, err2 := c.daddy.driver.GetTLSConfig()
		if err2 != nil {
			return c.writeMessage(550, fmt.Sprintf("Cannot get a TLS config: %v", err2))
		}

		listener = tls.NewListener(tcpListener, tlsConfig)
	} else {
		listener = tcpListener
	}

	p := &passiveTransferHandler{
		tcpListener: tcpListener,
		listener:    listener,
		Port:        tcpListener.Addr().(*net.TCPAddr).Port,
	}

	// TODO(fclairamb): Rewrite this part
	if c.command == "PASV" {
		p1 := p.Port / 256
		p2 := p.Port - (p1 * 256)
		// Provide our external IP address so the ftp client can connect back to us
		ip := c.daddy.settings.PublicHost

		// If we don't have an IP address, we can take the one that was used for the current connection
		if ip == "" {
			// Defer to the user provided resolver.
			if c.daddy.settings.PublicIPResolver != nil {
				ip, err = c.daddy.settings.PublicIPResolver(c)
				if err != nil {
					// Not sure if there is better desired behavior than this.
					// If we can't resolve the public ip to return to the client, is there any actual
					// fallback that is better than erroring.
					return err
				}
			} else {
				ip = strings.Split(c.conn.LocalAddr().String(), ":")[0]
			}
		}

		quads := strings.Split(ip, ".")
		err = c.writeMessage(227, fmt.Sprintf("Entering Passive Mode (%s,%s,%s,%s,%d,%d)", quads[0], quads[1], quads[2], quads[3], p1, p2))
	} else {
		err = c.writeMessage(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", p.Port))
	}

	c.setTransfer(p)

	return err
}

func (p *passiveTransferHandler) ConnectionWait(wait time.Duration) (net.Conn, error) {
	if p.connection == nil {
		if err := p.tcpListener.SetDeadline(time.Now().Add(wait)); err != nil {
			return nil, err
		}
		var err error
		p.connection, err = p.listener.Accept()
		if err != nil {
			return nil, err
		}
	}

	return p.connection, nil
}

func (p *passiveTransferHandler) Open() (net.Conn, error) {
	return p.ConnectionWait(time.Minute)
}

// Closing only the client connection is not supported at that time
func (p *passiveTransferHandler) Close() error {
	var err, err2 error
	if p.tcpListener != nil {
		err = p.tcpListener.Close()
	}
	if p.connection != nil {
		err = p.connection.Close()
	}
	if err != nil {
		return err
	}
	return err2
}
