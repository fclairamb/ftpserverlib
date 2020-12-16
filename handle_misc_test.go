package ftpserver

import (
	"strings"
	"testing"

	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func TestSiteCommand(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	rc, response, err := raw.SendCommand("SITE HELP")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc, "Are we supporting it now ?")
	require.Equal(t, "Not understood SITE subcommand", response, "Are we supporting it now ?")
}

// florent(2018-01-14): #58: IDLE timeout: Testing timeout
// drakkan(2020-12-12): idle time is broken if you set timeout to 1 minute
// and a transfer requires more than 1 minutes any command issued at the transfer end
// will timeout. I handle idle timeout myself in SFTPGo but you could be
// interested to fix this bug
func TestIdleTimeout(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{IdleTimeout: 2}})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

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
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	rc, str, err := raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)

	count := strings.Count(str, "\n")
	require.GreaterOrEqual(t, count, 4)
	require.NotEqual(t, ' ', str[0])
}

func TestCLNT(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	rc, _, err := raw.SendCommand("CLNT NcFTP 3.2.6 macosx10.15")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
}

func TestOPTSUTF8(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	for _, cmd := range []string{"OPTS UTF8", "OPTS UTF8 ON"} {
		rc, message, err := raw.SendCommand(cmd)
		require.NoError(t, err)
		require.Equal(t, StatusOK, rc)
		require.Equal(t, "I'm in UTF8 only anyway", message)
	}
}

func TestOPTSHASH(t *testing.T) {
	s := NewTestServerWithDriver(
		t,
		&TestServerDriver{
			Debug: true,
			Settings: &Settings{
				EnableHASH: true,
			},
		},
	)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error
	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

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
