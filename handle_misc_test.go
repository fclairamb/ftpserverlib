package ftpserver

import (
	"crypto/tls"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func TestSiteCommand(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, response, err := raw.SendCommand("SITE help")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc, "Are we supporting it now ?")
	require.Equal(t, "Unknown SITE subcommand: HELP", response, "Are we supporting it now ?")
}

// florent(2018-01-14): #58: IDLE timeout: Testing timeout
// drakkan(2020-12-12): idle time is broken if you set timeout to 1 minute
// and a transfer requires more than 1 minutes any command issued at the transfer end
// will timeout. I handle idle timeout myself in SFTPGo but you could be
// interested to fix this bug
func TestIdleTimeout(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{Debug: false, Settings: &Settings{IdleTimeout: 2}})
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

	time.Sleep(time.Second * 1) // < 2s : OK

	returnCode, _, err := raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	time.Sleep(time.Second * 3) // > 2s : Timeout

	returnCode, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, returnCode)
}

func TestStat(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	returnCode, str, err := raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, returnCode)

	count := strings.Count(str, "\n")
	require.GreaterOrEqual(t, count, 4)
	require.NotEqual(t, ' ', str[0])

	server.settings.DisableSTAT = true

	returnCode, str, err = raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, returnCode, str)
}

func TestCLNT(t *testing.T) {
	server := NewTestServer(t, false)
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

	rc, _, err := raw.SendCommand("CLNT NcFTP 3.2.6 macosx10.15")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
}

func TestOPTSUTF8(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	for _, cmd := range []string{"OPTS UTF8", "OPTS UTF8 ON"} {
		rc, message, err := raw.SendCommand(cmd)
		require.NoError(t, err)
		require.Equal(t, StatusOK, rc)
		require.Equal(t, "I'm in UTF8 only anyway", message)
	}
}

func TestOPTSHASH(t *testing.T) {
	server := NewTestServerWithTestDriver(
		t,
		&TestServerDriver{
			Debug: false,
			Settings: &Settings{
				EnableHASH: true,
			},
		},
	)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, message, err := raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, message)
	require.Equal(t, "SHA-256", message)

	returnCode, message, err = raw.SendCommand("OPTS HASH MD5")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
	require.Equal(t, "MD5", message)

	returnCode, message, err = raw.SendCommand("OPTS HASH CRC-37")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)
	require.Equal(t, "Unknown algorithm, current selection not changed", message)

	returnCode, message, err = raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
	require.Equal(t, "MD5", message)

	// now disable hash support
	server.settings.EnableHASH = false

	returnCode, _, err = raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)
}

func TestAVBL(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, response, err := raw.SendCommand("AVBL")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)
	require.Equal(t, "123", response)

	// a missing dir
	returnCode, _, err = raw.SendCommand("AVBL missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)

	// AVBL on a file path
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	returnCode, response, err = raw.SendCommand("AVBL file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)
	require.Equal(t, "/file: is not a directory", response)

	noavblDir, err := client.Mkdir("noavbl")
	require.NoError(t, err)

	returnCode, response, err = raw.SendCommand(fmt.Sprintf("AVBL %v", noavblDir))
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)
	require.Equal(t, fmt.Sprintf("Couldn't get space for path %v: %v", noavblDir, errAvblNotPermitted.Error()), response)
}

func TestQuit(t *testing.T) {
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
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("QUIT")
	require.NoError(t, err)
	require.Equal(t, StatusClosingControlConn, rc)
}

func TestQuitWithCustomMessage(t *testing.T) {
	driver := &MesssageDriver{
		TestServerDriver{
			Debug: true,
			TLS:   true,
		},
	}
	driver.Init()
	server := NewTestServerWithDriver(t, driver)
	req := require.New(t)
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
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	req.NoError(err, "Couldn't open raw connection")

	rc, msg, err := raw.SendCommand("QUIT")
	req.NoError(err)
	req.Equal(StatusClosingControlConn, rc)
	req.Equal("Sayonara, bye bye!", msg)
}

func TestQuitWithTransferInProgress(t *testing.T) {
	req := require.New(t)
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
	})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	syncChannel := make(chan struct{}, 1)
	go
	// I don't see a pragmatic/good way to test this without forwarding the errors to the channel,
	// and thus losing the convenience of testify.
	//nolint:testifylint
	func() {
		defer close(syncChannel)

		dcGetter, err := raw.PrepareDataConn() //nolint:govet
		req.NoError(err)

		file := createTemporaryFile(t, 256*1024)
		fileName := filepath.Base(file.Name())
		rc, response, err := raw.SendCommand(fmt.Sprintf("%s %s", "STOR", fileName))
		req.NoError(err)
		req.Equal(StatusFileStatusOK, rc, response)

		dataConn, err := dcGetter()
		req.NoError(err)

		syncChannel <- struct{}{}
		// wait some more time to be sure we send the QUIT command before starting the file copy
		time.Sleep(100 * time.Millisecond)

		_, err = io.Copy(dataConn, file)
		req.NoError(err)

		err = dataConn.Close()
		req.NoError(err)
	}()

	// wait for the transfer to start
	<-syncChannel
	// we send a QUIT command after sending STOR and before the transfer ends.
	// We expect the transfer close response and then the QUIT response
	returnCode, _, err := raw.SendCommand("QUIT")
	req.NoError(err)
	req.Equal(StatusClosingDataConn, returnCode)

	returnCode, _, err = raw.ReadResponse()
	req.NoError(err)
	req.Equal(StatusClosingControlConn, returnCode)
}

func TestTYPE(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("TYPE I")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE A N")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE i")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE a")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE l 8")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE l 7")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("TYPE wrong")
	require.NoError(t, err)
	require.Equal(t, StatusNotImplementedParam, returnCode)
}

func TestMode(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	returnCode, _, err := raw.SendCommand("MODE S")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	returnCode, _, err = raw.SendCommand("MODE X")
	require.NoError(t, err)
	require.Equal(t, StatusNotImplementedParam, returnCode)
}

func TestREIN(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	returnCode, _, err := raw.SendCommand("REIN")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, returnCode)
}

// Custom driver for testing ClientDriverExtensionSite
// Implements Site(param string) error
type customSiteDriver struct {
	TestServerDriver
}

func (d *customSiteDriver) Site(param string) error {
	switch param {
	case "CUSTOMERR":
		return fmt.Errorf("custom site error")
	case "PROCEED":
		return ErrProceedWithDefaultBehavior
	case "OK":
		return nil
	default:
		return ErrProceedWithDefaultBehavior
	}
}

func TestClientDriverExtensionSite(t *testing.T) {
	driver := &customSiteDriver{}
	driver.Init()
	server := NewTestServerWithDriver(t, driver)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")
	defer func() { panicOnError(client.Close()) }()
	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")
	defer func() { require.NoError(t, raw.Close()) }()

	// Custom error from Site
	rc, response, err := raw.SendCommand("SITE CUSTOMERR")
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom site error")

	// Default behavior fallback (should get unknown subcommand)
	rc, response, err = raw.SendCommand("SITE PROCEED")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
	require.Contains(t, response, "Unknown SITE subcommand")

	// Short-circuit: Site returns nil, so command is accepted (no error, no message)
	rc, response, err = raw.SendCommand("SITE OK")
	require.NoError(t, err)
	require.Equal(t, 0, len(response))
}
