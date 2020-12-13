package ftpserver

import (
	"fmt"
	"io/ioutil"
	"os"
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

func TestHASHCommand(t *testing.T) {
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

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	dir, err := c.Mkdir("testdir")
	require.NoError(t, err)

	tempFile, err := ioutil.TempFile("", "ftpserver")
	require.NoError(t, err)
	err = ioutil.WriteFile(tempFile.Name(), []byte("sample data with know checksum/hash\n"), os.ModePerm)
	require.NoError(t, err)

	crc32Sum := "21b0f382"
	sha256Hash := "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b"

	ftpUpload(t, c, tempFile, "file.txt")

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	// ask hash for a directory
	rc, _, err := raw.SendCommand(fmt.Sprintf("XSHA256 %v", dir))
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTakenNoFile, rc)

	// test the HASH command
	rc, message, err := raw.SendCommand("HASH file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("SHA-256 0-36 %v file.txt", sha256Hash)))

	// change algo and request the hash again
	rc, message, err = raw.SendCommand("OPTS HASH CRC32")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "CRC32", message)

	rc, message, err = raw.SendCommand("HASH file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("CRC32 0-36 %v file.txt", crc32Sum)))
}

func TestCustomHASHCommands(t *testing.T) {
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

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	tempFile, err := ioutil.TempFile("", "ftpserver")
	require.NoError(t, err)
	err = ioutil.WriteFile(tempFile.Name(), []byte("sample data with know checksum/hash\n"), os.ModePerm)
	require.NoError(t, err)

	ftpUpload(t, c, tempFile, "file.txt")

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	customCommands := make(map[string]string)
	customCommands["XCRC"] = "21b0f382"
	customCommands["MD5"] = "6905e38270e1797e68f69026bfbef131"
	customCommands["XMD5"] = "6905e38270e1797e68f69026bfbef131"
	customCommands["XSHA"] = "0f11c4103a2573b14edd4733984729f2380d99ed"
	customCommands["XSHA1"] = "0f11c4103a2573b14edd4733984729f2380d99ed"
	customCommands["XSHA256"] = "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b"
	customCommands["XSHA512"] = "4f95c20e4d030cbc43b1e139a0fe11c5e0e5e520cf3265bae852ae212b1c7cdb02c2fea5ba038cbf3202af8cdf313579fbe344d47919c288c16d6dd671e9db63" //nolint:lll

	var rc int
	var message string

	for cmd, expected := range customCommands {
		rc, message, err = raw.SendCommand(fmt.Sprintf("%v file.txt", cmd))
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)
		require.True(t, strings.HasSuffix(message, expected))
	}

	// now a partial hash
	rc, message, err = raw.SendCommand("XSHA256 file.txt 7 11")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc)
	require.True(t, strings.HasSuffix(message, "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7"))

	// invalid start
	rc, _, err = raw.SendCommand("XSHA256 file.txt a 11")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc)

	// invalid end
	rc, _, err = raw.SendCommand("XSHA256 file.txt 7 a")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc)
}
