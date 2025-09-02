package ftpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"path"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

const DirKnown = "known"

func TestGetRelativePaths(t *testing.T) {
	type relativePathTest struct {
		workingDir, path, result string
	}
	tests := []relativePathTest{
		{"/", "/p", "p"},
		{"/", "/", ""},
		{"/p", "/p", ""},
		{"/p", "/p1", "../p1"},
		{"/p", "/p/p1", "p1"},
		{"/p/p1", "/p/p2/p3", "../p2/p3"},
		{"/", "p", "p"},
	}

	handler := clientHandler{}

	for _, test := range tests {
		handler.SetPath(test.workingDir)
		result := handler.getRelativePath(test.path)
		require.Equal(t, test.result, result)
	}
}

func TestDirListing(t *testing.T) {
	// MLSD is disabled we relies on LIST of files listing
	server := NewTestServerWithTestDriver(t, &TestServerDriver{Debug: false, Settings: &Settings{DisableMLSD: true}})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	dirName, err := client.Mkdir(DirKnown)
	require.NoError(t, err, "Couldn't create dir")
	require.Equal(t, path.Join("/", DirKnown), dirName)

	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, DirKnown, contents[0].Name())

	// LIST also works for filePath
	fileName := "testfile.ext"

	_, err = client.ReadDir(fileName)
	require.Error(t, err, "LIST for not existing filePath must fail")

	ftpUpload(t, client, createTemporaryFile(t, 10), fileName)

	fileContents, err := client.ReadDir(fileName)
	require.NoError(t, err)
	require.Len(t, fileContents, 1)
	require.Equal(t, fileName, fileContents[0].Name())

	// the test driver will fail to open this dir
	dirName, err = client.Mkdir("fail-to-open-dir")
	require.NoError(t, err)
	_, err = client.ReadDir(dirName)
	require.Error(t, err)
}

func TestDirListingPathArg(t *testing.T) {
	// MLSD is disabled we relies on LIST of files listing
	server := NewTestServerWithTestDriver(t, &TestServerDriver{Debug: false, Settings: &Settings{DisableMLSD: true}})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	for _, dir := range []string{"/" + DirKnown, "/" + DirKnown + "/1"} {
		_, err = client.Mkdir(dir)
		require.NoError(t, err, "Couldn't create dir")
	}

	contents, err := client.ReadDir(DirKnown)
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, "1", contents[0].Name())

	contents, err = client.ReadDir("")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, DirKnown, contents[0].Name())
}

func TestDirHandling(t *testing.T) {
	s := NewTestServer(t, false)

	client, err := goftp.DialConfig(goftp.Config{
		User:     authUser,
		Password: authPass,
	}, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("CWD /unknown")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)

	_, err = client.Mkdir("/" + DirKnown)
	require.NoError(t, err)

	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)

	returnCode, _, err = raw.SendCommand("CWD /" + DirKnown)
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)

	testSubdir := ` strange\\ sub" dìr`
	returnCode, _, err = raw.SendCommand(fmt.Sprintf("MKD %v", testSubdir))
	require.NoError(t, err)
	require.Equal(t, StatusPathCreated, returnCode)

	returnCode, response, err := raw.SendCommand(fmt.Sprintf("CWD %v", testSubdir))
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode, response)

	returnCode, response, err = raw.SendCommand(fmt.Sprintf("PWD %v", testSubdir))
	require.NoError(t, err)
	require.Equal(t, StatusPathCreated, returnCode, response)
	require.Equal(t, `"/known/ strange\\ sub"" dìr" is the current directory`, response)

	returnCode, response, err = raw.SendCommand("CDUP")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode, response)

	err = client.Rmdir(path.Join("/", DirKnown, testSubdir))
	require.NoError(t, err)

	err = client.Rmdir(path.Join("/", DirKnown))
	require.NoError(t, err)

	err = client.Rmdir(path.Join("/", DirKnown))
	require.Error(t, err, "We shouldn't have been able to ftpDelete known again")
}

func TestCWDToRegularFile(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Getwd will send a PWD command
	p, err := client.Getwd()
	require.NoError(t, err)
	require.Equal(t, "/", p, "Bad path")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file.txt")

	rc, msg, err := raw.SendCommand("CWD /file.txt")
	require.NoError(t, err)
	require.Equal(t, `Can't change directory to /file.txt: Not a Directory`, msg)
	require.Equal(t, StatusActionNotTaken, rc, "We shouldn't have been able to CWD to a regular file")
}

func TestMkdirRmDir(t *testing.T) {
	server := NewTestServer(t, false)
	req := require.New(t)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	req.NoError(err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	t.Run("standard", func(t *testing.T) {
		returnCode, _, err := raw.SendCommand("SITE MKDIR /dir1/dir2/dir3")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, returnCode)

		for _, d := range []string{"/dir1", "/dir1/dir2", "/dir1/dir2/dir3"} {
			stat, errStat := client.Stat(d)
			require.NoError(t, errStat)
			require.True(t, stat.IsDir())
		}

		returnCode, _, err = raw.SendCommand("SITE RMDIR /dir1")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, returnCode)

		for _, d := range []string{"/dir1", "/dir1/dir2", "/dir1/dir2/dir3"} {
			stat, errStat := client.Stat(d)
			require.Error(t, errStat)
			require.Nil(t, stat)
		}

		_, err = client.Mkdir("/missing/path")
		require.Error(t, err)
	})

	t.Run("syntax error", func(t *testing.T) {
		returnCode, _, err := raw.SendCommand("SITE MKDIR")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)

		returnCode, _, err = raw.SendCommand("SITE RMDIR")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode)
	})

	t.Run("spaces", func(t *testing.T) {
		returnCode, _, err := raw.SendCommand("SITE MKDIR /dir1 /dir2")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, returnCode)

		{
			dir := "/dir1 /dir2"
			stat, errStat := client.Stat(dir)
			require.NoError(t, errStat)
			require.True(t, stat.IsDir())
		}

		returnCode, _, err = raw.SendCommand("SITE RMDIR /dir1 /dir2")
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, returnCode)
	})
}

// TestDirListingWithSpace uses the MLSD for files listing
func TestDirListingWithSpace(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	dirName := " with spaces "

	_, err = client.Mkdir(dirName)
	require.NoError(t, err, "Couldn't create dir")

	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, dirName, contents[0].Name())

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, response, err := raw.SendCommand("CWD /" + dirName)
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)
	require.Equal(t, "CD worked on /"+dirName, response)

	dcGetter, err := raw.PrepareDataConn()
	require.NoError(t, err)

	returnCode, response, err = raw.SendCommand("NLST /")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, returnCode, response)

	dc, err := dcGetter()
	require.NoError(t, err)
	resp, err := io.ReadAll(dc)
	require.NoError(t, err)
	require.Equal(t, "../"+dirName+"\r\n", string(resp))

	returnCode, _, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	returnCode, response, err = raw.SendCommand("NLST /missingpath")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, returnCode, response)
}

func TestCleanPath(t *testing.T) {
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

	// various path purity tests

	for _, dir := range []string{
		"..",
		"../..",
		"/../..",
		"////",
		"/./",
		"/././.",
	} {
		rc, response, err := raw.SendCommand("CWD " + dir)
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, rc)
		require.Equal(t, "CD worked on /", response)

		p, err := client.Getwd()
		require.NoError(t, err)
		require.Equal(t, "/", p)
	}
}

func TestTLSTransfer(t *testing.T) {
	req := require.New(t)
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		TLS:   true,
	})
	server.settings.TLSRequired = MandatoryEncryption

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
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	contents, err := client.ReadDir("/")
	req.NoError(err)
	req.Empty(contents)

	raw, err := client.OpenRawConn()
	req.NoError(err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, response, err := raw.SendCommand("PROT C")
	req.NoError(err)
	req.Equal(StatusOK, returnCode)
	req.Equal("OK", response)

	returnCode, _, err = raw.SendCommand("PASV")
	req.NoError(err)
	req.Equal(StatusEnteringPASV, returnCode)

	returnCode, response, err = raw.SendCommand("MLSD /")
	req.NoError(err)
	req.Equal(StatusServiceNotAvailable, returnCode)
	req.Equal("unable to open transfer: TLS is required", response)
}

func TestPerClientTLSTransfer(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug:          true,
		TLS:            true,
		TLSRequirement: MandatoryEncryption,
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
		TLSMode: goftp.TLSExplicit,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.ReadDir("/")
	require.NoError(t, err)

	// now switch to unencrypted data connection
	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, resp, err := raw.SendCommand("PROT C")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
	require.Equal(t, "OK", resp)

	returnCode, _, err = raw.SendCommand("PASV")
	require.NoError(t, err)
	require.Equal(t, StatusEnteringPASV, returnCode)

	returnCode, response, err := raw.SendCommand("MLSD /")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, returnCode)
	require.Equal(t, "unable to open transfer: TLS is required", response)
}

func TestDirListingBeforeLogin(t *testing.T) {
	s := NewTestServer(t, false)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", s.Addr())
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

	_, err = conn.Write([]byte("LIST\r\n"))
	require.NoError(t, err)

	readBytes, err = conn.Read(buf)
	require.NoError(t, err)

	response = string(buf[:readBytes])
	require.Equal(t, "530 Please login with USER and PASS\r\n", response)
}

func TestListArgs(t *testing.T) {
	t.Parallel()

	t.Run("with-mlsd", func(t *testing.T) {
		t.Parallel()
		testListDirArgs(
			t,
			NewTestServer(t, false),
		)
	})

	t.Run("without-mlsd", func(t *testing.T) {
		t.Parallel()
		testListDirArgs(
			t,
			NewTestServerWithTestDriver(t, &TestServerDriver{Debug: false, Settings: &Settings{DisableMLSD: true}}),
		)
	})
}

func testListDirArgs(t *testing.T, server *FtpServer) {
	t.Helper()
	req := require.New(t)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	testDir := "testdir"

	client, err := goftp.DialConfig(conf, server.Addr())
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	for _, arg := range supportedlistArgs {
		server.settings.DisableLISTArgs = true

		_, err = client.ReadDir(arg)
		require.Error(t, err, "list args are disabled \"list", arg, "\" must fail")

		server.settings.DisableLISTArgs = false

		contents, err := client.ReadDir(arg)
		req.NoError(err)
		req.Empty(contents)

		_, err = client.Mkdir(arg)
		req.NoError(err)

		_, err = client.Mkdir(fmt.Sprintf("%v/%v", arg, testDir))
		req.NoError(err)

		contents, err = client.ReadDir(arg)
		req.NoError(err)
		req.Len(contents, 1)
		req.Equal(contents[0].Name(), testDir)

		contents, err = client.ReadDir(fmt.Sprintf("%v %v", arg, arg))
		req.NoError(err)
		req.Len(contents, 1)
		req.Equal(contents[0].Name(), testDir)

		// cleanup
		err = client.Rmdir(fmt.Sprintf("%v/%v", arg, testDir))
		req.NoError(err)

		err = client.Rmdir(arg)
		req.NoError(err)
	}
}

func TestMLSDTimezone(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	ftpUpload(t, client, createTemporaryFile(t, 10), "file")
	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, "file", contents[0].Name())
	require.InDelta(t, time.Now().Unix(), contents[0].ModTime().Unix(), 5)
}

func TestMLSDAndNLSTFilePathError(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// MLSD shouldn't work for filePaths
	fileName := "testfile.ext"

	_, err = client.ReadDir(fileName)
	require.Error(t, err, "MLSD for not existing filePath must fail")

	ftpUpload(t, client, createTemporaryFile(t, 10), fileName)

	_, err = client.ReadDir(fileName)
	require.Error(t, err, "MLSD is enabled, MLSD for filePath must fail")

	// NLST should work for filePath
	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	dcGetter, err := raw.PrepareDataConn()
	require.NoError(t, err)

	rc, response, err := raw.SendCommand("NLST /../" + fileName)
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, rc, response)

	dc, err := dcGetter()
	require.NoError(t, err)
	resp, err := io.ReadAll(dc)
	require.NoError(t, err)
	require.Equal(t, fileName+"\r\n", string(resp))
}
