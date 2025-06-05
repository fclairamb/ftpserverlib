package ftpserver

import (
	"testing"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func TestSiteCommandsWithExtension(t *testing.T) {
	// Test with a driver that implements the SITE command extension
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

	// Test SITE CHMOD with extension
	returnCode, _, err := raw.SendCommand("SITE CHMOD 755 /")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	// Test SITE CHOWN with extension
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000:500 /")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	// Test SITE MKDIR with extension
	returnCode, _, err = raw.SendCommand("SITE MKDIR /testdir")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)

	// Test SITE RMDIR with extension
	returnCode, _, err = raw.SendCommand("SITE RMDIR /testdir")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)
}

func TestSiteCommandsWithoutExtension(t *testing.T) {
	// Test with a driver that does NOT implement the SITE command extension (fallback behavior)
	serverDriver := &TestServerDriverNoSiteExt{
		TestServerDriver: &TestServerDriver{Debug: false},
	}
	serverDriver.Init()
	server := NewTestServerWithTestDriver(t, serverDriver.TestServerDriver)

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

	// Test SITE CHMOD fallback
	returnCode, _, err := raw.SendCommand("SITE CHMOD 755 /")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	// Test SITE CHOWN fallback (should work with afero.Fs Chown method)
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000:500 /")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	// Test SITE MKDIR fallback
	returnCode, _, err = raw.SendCommand("SITE MKDIR /testdir2")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)

	// Test SITE RMDIR fallback
	returnCode, _, err = raw.SendCommand("SITE RMDIR /testdir2")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)
}

func TestSiteCommandErrors(t *testing.T) {
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

	// Test SITE CHMOD with invalid parameters
	returnCode, _, err := raw.SendCommand("SITE CHMOD")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)

	returnCode, _, err = raw.SendCommand("SITE CHMOD 755")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)

	returnCode, _, err = raw.SendCommand("SITE CHMOD invalid /")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)

	// Test SITE CHOWN with invalid parameters
	returnCode, _, err = raw.SendCommand("SITE CHOWN")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)

	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)

	// Test SITE CHOWN with invalid user/group
	returnCode, _, err = raw.SendCommand("SITE CHOWN 9999:9999 /")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)

	// Test SITE MKDIR with missing path
	returnCode, _, err = raw.SendCommand("SITE MKDIR")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)

	// Test SITE RMDIR with missing path
	returnCode, _, err = raw.SendCommand("SITE RMDIR")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)
}

func TestSiteCommandDisabled(t *testing.T) {
	// Test with SITE commands disabled
	serverDriver := &TestServerDriver{
		Debug: false,
		Settings: &Settings{
			DisableSite: true,
		},
	}
	serverDriver.Init()
	server := NewTestServerWithTestDriver(t, serverDriver)

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

	// Test that SITE commands are disabled
	returnCode, response, err := raw.SendCommand("SITE CHMOD 755 /")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)
	require.Equal(t, "SITE support is disabled", response)
}