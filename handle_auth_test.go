package ftpserver

import (
	"crypto/tls"
	"net"
	"net/textproto"
	"sync/atomic"
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
	server := NewTestServer(t, false)
	// send a NOOP before the login, this doesn't seems possible using secsy/goftp so use the old way ...
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", server.Addr())
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 1024)
	readBytes, err := conn.Read(buf)
	require.NoError(t, err)

	response := string(buf[:readBytes])
	require.Equal(t, "220 TEST Server\r\n", response)

	_, err = conn.Write([]byte("NOOP\r\n"))
	require.NoError(t, err)

	readBytes, err = conn.Read(buf)
	require.NoError(t, err)

	response = string(buf[:readBytes])
	require.Equal(t, "200 OK\r\n", response)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Couldn't NOOP")

	returnCode, response, err = raw.SendCommand("SYST")
	require.NoError(t, err)
	require.Equal(t, StatusSystemType, returnCode)
	require.Equal(t, "UNIX Type: L8", response)

	server.settings.DisableSYST = true
	returnCode, response, err = raw.SendCommand("SYST")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, returnCode, response)
}

func TestLoginFailure(t *testing.T) {
	server := NewTestServer(t, false)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass + "_wrong",
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	require.Error(t, err, "We should have failed to login")
}

func TestLoginCustom(t *testing.T) {
	req := require.New(t)
	driver := &MesssageDriver{}
	driver.Init()
	server := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass + "_wrong",
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	req.Error(err, "We should have failed to login")
}

func TestLoginNil(t *testing.T) {
	server := NewTestServer(t, true)
	req := require.New(t)

	conf := goftp.Config{
		User:     nilUser,
		Password: nilUser,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	req.Error(err)
}

func TestAuthTLS(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		TLS:   true,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't upgrade connection to TLS")

	err = raw.Close()
	require.NoError(t, err)
}

func TestAuthExplicitTLSFailure(t *testing.T) {
	server := NewTestServer(t, false)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	_, err = c.OpenRawConn()
	require.Error(t, err, "Upgrade to TLS should fail, TLS is not configured server side")
}

func TestAuthTLSRequired(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		TLS:   true,
	})
	server.settings.TLSRequired = MandatoryEncryption

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	require.Error(t, err, "Plain text login must fail, TLS is required")
	require.EqualError(t, err, "unexpected response: 421-TLS is required")

	conf.TLSConfig = &tls.Config{
		//nolint:gosec
		InsecureSkipVerify: true,
	}
	conf.TLSMode = goftp.TLSExplicit

	client, err = goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand(cmdSTAT)
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, rc)
}

func TestAuthTLSVerificationFailed(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug:                true,
		TLS:                  true,
		TLSVerificationReply: tlsVerificationFailed,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	require.Error(t, err, "TLS verification shoul fail")
}

func TestAuthTLSCertificate(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug:                true,
		TLS:                  true,
		TLSVerificationReply: tlsVerificationAuthenticated,
	})

	conf := goftp.Config{
		User: authUser,
		TLSConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand(cmdSTAT)
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, rc)
}

func TestAuthPerClientTLSRequired(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug:          true,
		TLS:            true,
		TLSRequirement: MandatoryEncryption,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.OpenRawConn()
	require.Error(t, err, "Plain text login must fail, TLS is required")
	require.EqualError(t, err, "unexpected response: 421-TLS is required")

	conf.TLSConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec
	}
	conf.TLSMode = goftp.TLSExplicit

	client, err = goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand(cmdSTAT)
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, rc)
}

func TestUserVerifierError(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		TLS:   true,
		// setting an invalid TLS requirement will cause the test driver
		// to return an error in PreAuthUser
		TLSRequirement: -1,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
		TLSMode: goftp.TLSExplicit,
	}

	c, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	_, err = c.OpenRawConn()
	require.Error(t, err, "Plain text login must fail, TLS is required")
	require.EqualError(t, err, "unexpected response: 530-User rejected: invalid TLS requirement")
}

func dialRawControl(t *testing.T, addr string) *textproto.Conn {
	t.Helper()

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	require.NoError(t, err)
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	ctrl := textproto.NewConn(conn)

	t.Cleanup(func() { _ = ctrl.Close() })

	_, _, err = ctrl.ReadResponse(StatusServiceReady)
	require.NoError(t, err)

	return ctrl
}

func sendRawCommand(ctrl *textproto.Conn, cmd string) (int, error) {
	if _, err := ctrl.Cmd("%s", cmd); err != nil {
		return 0, err
	}

	code, _, err := ctrl.ReadResponse(0)

	return code, err
}

// tokenAuthProvider makes the driver accept any user name, empty included,
// with the password "token", and reports whether AuthUser was called.
func tokenAuthProvider(driver *TestServerDriver) *atomic.Bool {
	called := &atomic.Bool{}
	driver.AuthProvider = func(_, pass string) (ClientDriver, error) {
		called.Store(true)

		if pass == "token" {
			return NewTestClientDriver(driver), nil
		}

		return nil, errBadUserNameOrPassword
	}

	return called
}

func TestPassWithoutUserTLSRequired(t *testing.T) {
	t.Parallel()

	driver := &TestServerDriver{Debug: false, TLS: true}
	authCalled := tokenAuthProvider(driver)
	server := NewTestServerWithTestDriver(t, driver)
	server.settings.TLSRequired = MandatoryEncryption

	ctrl := dialRawControl(t, server.Addr())

	code, err := sendRawCommand(ctrl, "PASS token")
	require.NoError(t, err)
	require.NotEqual(t, StatusUserLoggedIn, code)
	require.Equal(t, StatusServiceNotAvailable, code)
	require.False(t, authCalled.Load())

	code, err = sendRawCommand(ctrl, "PWD")
	if err == nil {
		require.NotEqual(t, StatusPathCreated, code)
	}
}

func TestPassWithoutUser(t *testing.T) {
	t.Parallel()

	driver := &TestServerDriver{Debug: false}
	authCalled := tokenAuthProvider(driver)
	server := NewTestServerWithTestDriver(t, driver)

	ctrl := dialRawControl(t, server.Addr())

	code, err := sendRawCommand(ctrl, "PASS token")
	require.NoError(t, err)
	require.Equal(t, StatusBadCommandSequence, code)
	require.False(t, authCalled.Load())

	code, err = sendRawCommand(ctrl, "PWD")
	require.NoError(t, err)
	require.Equal(t, StatusNotLoggedIn, code)

	// A regular USER/PASS sequence still logs in
	code, err = sendRawCommand(ctrl, "USER "+authUser)
	require.NoError(t, err)
	require.Equal(t, StatusUserOK, code)

	code, err = sendRawCommand(ctrl, "PASS token")
	require.NoError(t, err)
	require.Equal(t, StatusUserLoggedIn, code)
	require.True(t, authCalled.Load())

	code, err = sendRawCommand(ctrl, "PWD")
	require.NoError(t, err)
	require.Equal(t, StatusPathCreated, code)
}
