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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteCommand(t *testing.T) {
	s := NewTestServer(t, false)
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
	s := NewTestServerWithTestDriver(t, &TestServerDriver{Debug: false, Settings: &Settings{IdleTimeout: 2}})
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

	time.Sleep(time.Second * 1) // < 2s : OK

	rc, _, err := raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	time.Sleep(time.Second * 3) // > 2s : Timeout

	rc, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, rc)
}

func TestStat(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	rc, str, err := raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, rc)

	count := strings.Count(str, "\n")
	require.GreaterOrEqual(t, count, 4)
	require.NotEqual(t, ' ', str[0])

	s.settings.DisableSTAT = true

	rc, str, err = raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, rc, str)
}

func TestCLNT(t *testing.T) {
	s := NewTestServer(t, false)
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

	rc, _, err := raw.SendCommand("CLNT NcFTP 3.2.6 macosx10.15")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
}

func TestOPTSUTF8(t *testing.T) {
	s := NewTestServer(t, false)
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

	for _, cmd := range []string{"OPTS UTF8", "OPTS UTF8 ON"} {
		rc, message, err := raw.SendCommand(cmd)
		require.NoError(t, err)
		require.Equal(t, StatusOK, rc)
		require.Equal(t, "I'm in UTF8 only anyway", message)
	}
}

func TestOPTSHASH(t *testing.T) {
	s := NewTestServerWithTestDriver(
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

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, message, err := raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "SHA-256", message)

	rc, message, err = raw.SendCommand("OPTS HASH MD5")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "MD5", message)

	rc, message, err = raw.SendCommand("OPTS HASH CRC-37")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc)
	require.Equal(t, "Unknown algorithm, current selection not changed", message)

	rc, message, err = raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "MD5", message)

	// now disable hash support
	s.settings.EnableHASH = false

	rc, _, err = raw.SendCommand("OPTS HASH")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
}

func TestAVBL(t *testing.T) {
	s := NewTestServer(t, false)
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

	rc, response, err := raw.SendCommand("AVBL")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)
	require.Equal(t, "123", response)

	// a missing dir
	rc, _, err = raw.SendCommand("AVBL missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)

	// AVBL on a file path
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	rc, response, err = raw.SendCommand("AVBL file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, "/file: is not a directory", response)

	noavblDir, err := c.Mkdir("noavbl")
	require.NoError(t, err)

	rc, response, err = raw.SendCommand(fmt.Sprintf("AVBL %v", noavblDir))
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, fmt.Sprintf("Couldn't get space for path %v: %v", noavblDir, errAvblNotPermitted.Error()), response)
}

func TestQuit(t *testing.T) {
	s := NewTestServerWithTestDriver(t, &TestServerDriver{
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
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("QUIT")
	require.NoError(t, err)
	require.Equal(t, StatusClosingControlConn, rc)
}

func TestQuitWithCustomMessage(_t *testing.T) {
	d := &MesssageDriver{
		TestServerDriver{
			Debug: true,
			TLS:   true,
		},
	}
	d.Init()
	s := NewTestServerWithDriver(_t, d)
	t := require.New(_t)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		},
		TLSMode: goftp.TLSExplicit,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	t.NoError(err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	t.NoError(err, "Couldn't open raw connection")

	rc, msg, err := raw.SendCommand("QUIT")
	t.NoError(err)
	t.Equal(StatusClosingControlConn, rc)
	t.Equal("Sayonara, bye bye!", msg)
}

func TestQuitWithTransferInProgress(t *testing.T) {
	s := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
	})
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

	ch := make(chan struct{}, 1)
	go func() {
		defer close(ch)

		dcGetter, err := raw.PrepareDataConn() //nolint:govet
		require.NoError(t, err)
		file := createTemporaryFile(t, 256*1024)
		fileName := filepath.Base(file.Name())
		rc, response, err := raw.SendCommand(fmt.Sprintf("%s %s", "STOR", fileName))
		require.NoError(t, err)
		require.Equal(t, StatusFileStatusOK, rc, response)

		dc, err := dcGetter()
		assert.NoError(t, err)

		ch <- struct{}{}
		// wait some more time to be sure we send the QUIT command before starting the file copy
		time.Sleep(100 * time.Millisecond)

		_, err = io.Copy(dc, file)
		assert.NoError(t, err)

		err = dc.Close()
		assert.NoError(t, err)
	}()

	// wait for the trasfer to start
	<-ch
	// we send a QUIT command after sending STOR and before the transfer ends.
	// We expect the transfer close response and then the QUIT response
	rc, _, err := raw.SendCommand("QUIT")
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, rc)

	rc, _, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingControlConn, rc)
}

func TestTYPE(t *testing.T) {
	s := NewTestServer(t, false)
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

	rc, _, err := raw.SendCommand("TYPE I")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE A N")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE i")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE a")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE l 8")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE l 7")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)

	rc, _, err = raw.SendCommand("TYPE wrong")
	require.NoError(t, err)
	require.Equal(t, StatusNotImplementedParam, rc)
}
