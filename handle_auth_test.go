package ftpserver

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func panicOnError(err error) {
	if err != nil {
		panic(err)
	}
}

func TestLoginSuccess(t *testing.T) {
	s := NewTestServer(t, true)
	// send a NOOP before the login, this doesn't seems possible using secsy/goftp so use the old way ...
	conn, err := net.DialTimeout("tcp", s.Addr(), 5*time.Second)
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:n])
	require.Equal(t, "220 TEST Server\r\n", response)

	_, err = conn.Write([]byte("NOOP\r\n"))
	require.NoError(t, err)

	n, err = conn.Read(buf)
	require.NoError(t, err)

	response = string(buf[:n])
	require.Equal(t, "200 OK\r\n", response)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Couldn't NOOP")

	rc, response, err = raw.SendCommand("SYST")
	require.NoError(t, err)
	require.Equal(t, StatusSystemType, rc)
	require.Equal(t, "UNIX Type: L8", response)

	s.settings.DisableSYST = true
	rc, response, err = raw.SendCommand("SYST")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, rc, response)
}

func TestLoginFailure(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass + "_wrong",
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	_, err = c.OpenRawConn()
	require.Error(t, err, "We should have failed to login")
}

func TestAuthTLS(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			// nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't upgrade connection to TLS")

	err = raw.Close()
	require.NoError(t, err)
}

func TestAuthExplicitTLSFailure(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			// nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	_, err = c.OpenRawConn()
	require.Error(t, err, "Upgrade to TLS should fail, TLS is not configured server side")
}

func TestAuthTLSRequired(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})
	s.settings.TLSRequired = MandatoryEncryption

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	_, err = c.OpenRawConn()
	require.Error(t, err, "Plain text login must fail, TLS is required")
	require.EqualError(t, err, "unexpected response: 421-TLS is required")

	conf.TLSConfig = &tls.Config{
		// nolint:gosec
		InsecureSkipVerify: true,
	}
	conf.TLSMode = goftp.TLSExplicit

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, rc)
}
