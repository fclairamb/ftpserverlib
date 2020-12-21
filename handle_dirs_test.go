package ftpserver

import (
	"crypto/tls"
	"fmt"
	"net"
	"path"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

const DirKnown = "known"

func TestDirListing(t *testing.T) {
	// MLSD is disabled we relies on LIST of files listing
	s := NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	dirName, err := c.Mkdir(DirKnown)
	require.NoError(t, err, "Couldn't create dir")
	require.Equal(t, path.Join("/", DirKnown), dirName)

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, DirKnown, contents[0].Name())
}

func TestDirListingPathArg(t *testing.T) {
	// MLSD is disabled we relies on LIST of files listing
	s := NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	for _, dir := range []string{"/" + DirKnown, "/" + DirKnown + "/1"} {
		_, err = c.Mkdir(dir)
		require.NoError(t, err, "Couldn't create dir")
	}

	contents, err := c.ReadDir(DirKnown)
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, "1", contents[0].Name())

	contents, err = c.ReadDir("")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, DirKnown, contents[0].Name())
}

func TestDirHandling(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Getwd will send a PWD command
	p, err := c.Getwd()
	require.NoError(t, err)
	require.Equal(t, "/", p, "Bad path")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("CWD /unknown")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)

	_, err = c.Mkdir("/" + DirKnown)
	require.NoError(t, err)

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)

	rc, _, err = raw.SendCommand("CWD /" + DirKnown)
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc)

	testSubdir := " strange\\ sub dìr"
	rc, _, err = raw.SendCommand(fmt.Sprintf("MKD %v", testSubdir))
	require.NoError(t, err)
	require.Equal(t, StatusPathCreated, rc)

	rc, response, err := raw.SendCommand(fmt.Sprintf("CWD %v", testSubdir))
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc, response)

	rc, response, err = raw.SendCommand("CDUP")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc, response)

	err = c.Rmdir(path.Join("/", DirKnown, testSubdir))
	require.NoError(t, err)

	err = c.Rmdir(path.Join("/", DirKnown))
	require.NoError(t, err)

	err = c.Rmdir("/" + DirKnown)
	require.Error(t, err, "We shouldn't have been able to ftpDelete known again")
}

func TestMkdirRmDir(t *testing.T) {
	s := NewTestServer(t, true)
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

	t.Run("standard", func(t *testing.T) {
		rc, _, err := raw.SendCommand("SITE MKDIR /dir1/dir2/dir3")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)

		for _, d := range []string{"/dir1", "/dir1/dir2", "/dir1/dir2/dir3"} {
			stat, errStat := c.Stat(d)
			require.NoError(t, errStat)
			require.True(t, stat.IsDir())
		}

		rc, _, err = raw.SendCommand("SITE RMDIR /dir1")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)

		for _, d := range []string{"/dir1", "/dir1/dir2", "/dir1/dir2/dir3"} {
			stat, errStat := c.Stat(d)
			require.Error(t, errStat)
			require.Nil(t, stat)
		}

		_, err = c.Mkdir("/missing/path")
		require.Error(t, err)
	})

	t.Run("syntax error", func(t *testing.T) {
		rc, _, err := raw.SendCommand("SITE MKDIR")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorNotRecognised, rc)

		rc, _, err = raw.SendCommand("SITE RMDIR")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
	})

	t.Run("spaces", func(t *testing.T) {
		rc, _, err := raw.SendCommand("SITE MKDIR /dir1 /dir2")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)

		{
			dir := "/dir1 /dir2"
			stat, errStat := c.Stat(dir)
			require.NoError(t, errStat)
			require.True(t, stat.IsDir())
		}

		rc, _, err = raw.SendCommand("SITE RMDIR /dir1 /dir2")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)
	})
}

// TestDirListingWithSpace uses the MLSD for files listing
func TestDirListingWithSpace(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	dirName := " with spaces "

	_, err = c.Mkdir(dirName)
	require.NoError(t, err, "Couldn't create dir")

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, dirName, contents[0].Name())

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, response, err := raw.SendCommand(fmt.Sprintf("CWD /%s", dirName))
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc)
	require.Equal(t, fmt.Sprintf("CD worked on /%s", dirName), response)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	rc, response, err = raw.SendCommand("NLST /")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, rc, response)

	rc, _, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, rc)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	rc, response, err = raw.SendCommand("NLST /missingpath")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, rc, response)
}

func TestCleanPath(t *testing.T) {
	s := NewTestServer(t, true)
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

	// various path purity tests

	for _, dir := range []string{
		"..",
		"../..",
		"/../..",
		"////",
		"/./",
		"/././.",
	} {
		rc, response, err := raw.SendCommand(fmt.Sprintf("CWD %s", dir))
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)
		require.Equal(t, "CD worked on /", response)

		p, err := c.Getwd()
		require.NoError(t, err)
		require.Equal(t, "/", p)
	}
}

func TestTLSTransfer(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})
	s.settings.TLSRequired = MandatoryEncryption

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

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 0)

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, response, err := raw.SendCommand("PROT C")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc)
	require.Equal(t, "OK", response)

	rc, _, err = raw.SendCommand("PASV")
	require.NoError(t, err)
	require.Equal(t, StatusEnteringPASV, rc)

	rc, response, err = raw.SendCommand("MLSD /")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, rc)
	require.Equal(t, "unable to open transfer: TLS is required", response)
}

func TestDirListingBeforeLogin(t *testing.T) {
	s := NewTestServer(t, true)
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

	_, err = conn.Write([]byte("LIST\r\n"))
	require.NoError(t, err)

	n, err = conn.Read(buf)
	require.NoError(t, err)

	response = string(buf[:n])
	require.Equal(t, "530 Please login with USER and PASS\r\n", response)
}

func TestListArgs(t *testing.T) {
	t.Run("with-mlsd", func(t *testing.T) {
		testListDirArgs(t, NewTestServer(t, true))
	})

	t.Run("without-mlsd", func(t *testing.T) {
		testListDirArgs(t, NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}}))
	})
}

func testListDirArgs(t *testing.T, s *FtpServer) {
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	testDir := "testdir"

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	for _, arg := range supportedlistArgs {
		s.settings.DisableLISTArgs = true

		_, err = c.ReadDir(arg)
		require.Error(t, err, fmt.Sprintf("list args are disabled \"list %v\" must fail", arg))

		s.settings.DisableLISTArgs = false

		contents, err := c.ReadDir(arg)
		require.NoError(t, err)
		require.Len(t, contents, 0)

		_, err = c.Mkdir(arg)
		require.NoError(t, err)

		_, err = c.Mkdir(fmt.Sprintf("%v/%v", arg, testDir))
		require.NoError(t, err)

		contents, err = c.ReadDir(arg)
		require.NoError(t, err)
		require.Len(t, contents, 1)
		require.Equal(t, contents[0].Name(), testDir)

		contents, err = c.ReadDir(fmt.Sprintf("%v %v", arg, arg))
		require.NoError(t, err)
		require.Len(t, contents, 1)
		require.Equal(t, contents[0].Name(), testDir)

		// cleanup
		err = c.Rmdir(fmt.Sprintf("%v/%v", arg, testDir))
		require.NoError(t, err)

		err = c.Rmdir(arg)
		require.NoError(t, err)
	}
}
