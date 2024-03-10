package ftpserver

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	lognoop "github.com/fclairamb/go-log/noop"
	"github.com/secsy/goftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getABORCmd() string {
	runes := []rune{}
	runes = append(runes, rune(242), rune(255))
	str := string(runes)

	return str + "ABOR"
}

func createTemporaryFile(t *testing.T, targetSize int) *os.File {
	t.Helper()

	var file *os.File

	var fileErr error

	file, fileErr = os.CreateTemp("", "ftpserver")
	require.NoError(t, fileErr, "Temporary file creation error")

	//nolint: gosec
	src := rand.New(rand.NewSource(0))
	_, err := io.CopyN(file, src, int64(targetSize))
	require.NoError(t, err, "Couldn't copy")

	t.Cleanup(func() {
		err = file.Close()
		assert.NoError(t, err, fmt.Sprintf("Problem closing file %#v", file.Name()))
		err := os.Remove(file.Name())
		require.NoError(t, err, fmt.Sprintf("Problem deleting file %#v", file.Name()))
	})

	return file
}

func hashFile(t *testing.T, file *os.File) string {
	t.Helper()

	_, err := file.Seek(0, 0)
	require.NoError(t, err, "Couldn't seek")

	hashser := sha256.New()
	_, err = io.Copy(hashser, file)
	require.NoError(t, err, "Couldn't hashUpload")

	hash := hex.EncodeToString(hashser.Sum(nil))

	_, err = file.Seek(0, 0)
	require.NoError(t, err, "Couldn't seek")

	return hash
}

func ftpUpload(t *testing.T, ftp *goftp.Client, file io.ReadSeeker, filename string) {
	t.Helper()

	_, err := file.Seek(0, 0)
	require.NoError(t, err, "Couldn't seek")

	err = ftp.Store(filename+".tmp", file)
	require.NoError(t, err, "Couldn't upload bin")

	err = ftp.Rename(filename+".tmp", filename)
	require.NoError(t, err, "Can't rename file")

	_, err = ftp.Stat(filename)
	require.NoError(t, err, "Couldn't get the size of file1.bin")

	if stats, err := ftp.Stat(filename); err != nil {
		// That's acceptable for now
		t.Log("Couldn't stat file:", err)
	} else {
		found := false
		if strings.HasSuffix(stats.Name(), filename) {
			found = true
		}

		if !found {
			t.Fatal("STAT: Couldn't find file !")
		}
	}
}

func ftpDownloadAndHash(t *testing.T, ftp *goftp.Client, filename string) string {
	t.Helper()

	hasher := sha256.New()
	err := ftp.Retrieve(filename, hasher)
	require.NoError(t, err, "Couldn't fetch file")

	return hex.EncodeToString(hasher.Sum(nil))
}

func ftpDownloadAndHashWithRawConnection(t *testing.T, raw goftp.RawConn, fileName string) string {
	t.Helper()

	req := require.New(t)
	hasher := sha256.New()

	dcGetter, err := raw.PrepareDataConn()
	req.NoError(err)

	returnCode, response, err := raw.SendCommand(fmt.Sprintf("RETR %v", fileName))
	require.NoError(t, err)
	req.Equal(StatusFileStatusOK, returnCode, response)

	dataConn, err := dcGetter()
	req.NoError(err)

	_, err = io.Copy(hasher, dataConn)
	req.NoError(err)

	err = dataConn.Close()
	req.NoError(err)

	returnCode, response, err = raw.ReadResponse()
	req.NoError(err)
	req.Equal(StatusClosingDataConn, returnCode, response)

	return hex.EncodeToString(hasher.Sum(nil))
}

func ftpUploadWithRawConnection(t *testing.T, raw goftp.RawConn, file io.Reader, fileName string, appendFile bool) {
	t.Helper()

	req := require.New(t)
	dcGetter, err := raw.PrepareDataConn()
	req.NoError(err)

	cmd := "STOR"
	if appendFile {
		cmd = "APPE"
	}

	returnCode, response, err := raw.SendCommand(fmt.Sprintf("%v %v", cmd, fileName))
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, returnCode, response)

	dataConn, err := dcGetter()
	req.NoError(err)

	_, err = io.Copy(dataConn, file)
	req.NoError(err)

	err = dataConn.Close()
	req.NoError(err)

	returnCode, response, err = raw.ReadResponse()
	req.NoError(err)
	assert.Equal(t, StatusClosingDataConn, returnCode, response)
}

func ftpDelete(t *testing.T, ftp *goftp.Client, filename string) {
	t.Helper()

	err := ftp.Delete(filename)
	require.NoError(t, err, "Couldn't delete file "+filename)

	err = ftp.Delete(filename)
	require.Error(t, err, "Should have had a problem deleting "+filename)
}

func TestTransferIPv6(t *testing.T) {
	t.Parallel()

	createServer := func() *FtpServer {
		server := NewTestServerWithTestDriver(
			t,
			&TestServerDriver{
				Debug: false,
				Settings: &Settings{
					ActiveTransferPortNon20: true,
					ListenAddr:              "[::1]:0",
				},
			},
		)

		if server == nil {
			t.Skip("IPv6 is not supported here")
		}

		return server
	}

	t.Run("active", func(t *testing.T) {
		t.Parallel()

		s := createServer()
		testTransferOnConnection(t, s, true, false, false)
	})
	t.Run("passive", func(t *testing.T) {
		t.Parallel()

		s := createServer()
		testTransferOnConnection(t, s, false, false, false)
	})
}

// TestTransfer validates the upload of file in both active and passive mode
func TestTransfer(t *testing.T) {
	t.Parallel()

	t.Run("without-tls", func(t *testing.T) {
		t.Parallel()
		server := NewTestServerWithTestDriver(
			t,
			&TestServerDriver{
				Debug: false,
				Settings: &Settings{
					ActiveTransferPortNon20: true,
				},
			},
		)

		testTransferOnConnection(t, server, false, false, false)
		testTransferOnConnection(t, server, true, false, false)
	})
	t.Run("with-tls", func(t *testing.T) {
		t.Parallel()
		server := NewTestServerWithTestDriver(
			t,
			&TestServerDriver{
				Debug: false,
				TLS:   true,
				Settings: &Settings{
					ActiveTransferPortNon20: true,
				},
			},
		)

		testTransferOnConnection(t, server, false, true, false)
		testTransferOnConnection(t, server, true, true, false)
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		t.Parallel()
		server := NewTestServerWithTestDriver(t, &TestServerDriver{
			Debug: false,
			TLS:   true,
			Settings: &Settings{
				ActiveTransferPortNon20: true,
				TLSRequired:             ImplicitEncryption,
			},
		})

		testTransferOnConnection(t, server, false, true, true)
		testTransferOnConnection(t, server, true, true, true)
	})
}

func testTransferOnConnection(t *testing.T, server *FtpServer, active, enableTLS, implicitTLS bool) {
	t.Helper()

	conf := goftp.Config{
		User:            authUser,
		Password:        authPass,
		ActiveTransfers: active,
	}
	if enableTLS {
		conf.TLSConfig = &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: true,
		}
		if implicitTLS {
			conf.TLSMode = goftp.TLSImplicit
		} else {
			conf.TLSMode = goftp.TLSExplicit
		}
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	var hashUpload, hashDownload string
	{ // We create a 10MB file and upload it
		file := createTemporaryFile(t, 10*1024*1024)
		hashUpload = hashFile(t, file)
		ftpUpload(t, client, file, "file.bin")
	}

	{ // We download the file we just uploaded
		hashDownload = ftpDownloadAndHash(t, client, "file.bin")
		ftpDelete(t, client, "file.bin")
	}

	// We make sure the hashes of the two files match
	require.Equal(t, hashUpload, hashDownload, "The two files don't have the same hash")
}

func TestActiveModeDisabled(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: false,
		Settings: &Settings{
			ActiveTransferPortNon20: true,
			DisableActiveMode:       true,
		},
	})

	conf := goftp.Config{
		User:            authUser,
		Password:        authPass,
		ActiveTransfers: true,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	file := createTemporaryFile(t, 10*1024)
	err = client.Store("file.bin", file)
	require.Error(t, err, "active mode is disabled, upload must fail")
	require.True(t, strings.Contains(err.Error(), "421-PORT command is disabled"))
}

// TestFailedTransfer validates the handling of failed transfer caused by file access issues
func TestFailedTransfer(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// We create a 1KB file and upload it
	file := createTemporaryFile(t, 1*1024)
	err = client.Store("/non/existing/path/file.bin", file)
	require.Error(t, err, "This upload should have failed")

	err = client.Store("file.bin", file)
	require.NoError(t, err, "This upload should have succeeded")
}

func TestBogusTransferStart(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	returnCode, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, returnCode.Close()) }()

	{ // Completely bogus port declaration
		status, resp, err := returnCode.SendCommand("PORT something")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
	}

	{ // Completely bogus port declaration
		status, resp, err := returnCode.SendCommand("EPRT something")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
	}

	{ // Bad port number: 0
		status, resp, err := returnCode.SendCommand("EPRT |2|::1|0|")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
	}

	{ // Bad IP
		status, resp, err := returnCode.SendCommand("EPRT |1|253.254.255.256|2000|")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
	}

	{ // Bad protocol type: 3
		status, resp, err := returnCode.SendCommand("EPRT |3|::1|2000|")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
	}

	{ // good request but unacceptable ip address
		status, resp, err := returnCode.SendCommand("EPRT |1|::1|2000|")
		require.NoError(t, err)
		require.Equal(t, StatusSyntaxErrorParameters, status, resp)
		require.Contains(t, resp, "Your request does not meet the configured security requirements")
	}

	server.settings.ActiveConnectionsCheck = IPMatchDisabled

	{ // We end-up on a positive note
		status, resp, err := returnCode.SendCommand("EPRT |1|::1|2000|")
		require.NoError(t, err)
		require.Equal(t, StatusOK, status, resp)
	}
}

func TestFailingFileTransfer(t *testing.T) {
	t.Parallel()

	createClientOnServer := func(t *testing.T) (*goftp.Client, *os.File) {
		t.Helper()

		driver := &TestServerDriver{
			Debug: false,
		}

		server := NewTestServerWithTestDriver(t, driver)
		conf := goftp.Config{
			User:     authUser,
			Password: authPass,
		}

		file := createTemporaryFile(t, 1*1024)

		client, err := goftp.DialConfig(conf, server.Addr())
		require.NoError(t, err)
		t.Cleanup(func() { panicOnError(client.Close()) })

		return client, file
	}

	t.Run("on write", func(t *testing.T) {
		t.Parallel()
		c, file := createClientOnServer(t)
		err := c.Store("fail-to-write.bin", file)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), errFailWrite.Error()), err)
	})

	t.Run("on close", func(t *testing.T) {
		t.Parallel()
		c, file := createClientOnServer(t)
		err := c.Store("fail-to-close.bin", file)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), errFailClose.Error()), err)
	})

	t.Run("on seek", func(t *testing.T) {
		t.Parallel()
		client, _ := createClientOnServer(t)
		initialData := []byte("initial data")
		appendFile, err := os.CreateTemp("", "ftpserver")
		require.NoError(t, err)

		_, err = appendFile.Write(initialData)
		require.NoError(t, err)

		err = client.Store("fail-to-seek.bin", appendFile)
		require.NoError(t, err)

		err = appendFile.Close()
		require.NoError(t, err)

		appendFile, err = os.OpenFile(appendFile.Name(), os.O_APPEND|os.O_WRONLY, os.ModePerm)
		require.NoError(t, err)

		data := []byte("some more data")
		_, err = io.Copy(appendFile, bytes.NewReader(data))
		require.NoError(t, err)
		info, err := appendFile.Stat()
		require.NoError(t, err)
		require.Equal(t, int64(len(initialData)+len(data)), info.Size())
		_, err = client.TransferFromOffset("fail-to-seek.bin", nil, appendFile, int64(len(initialData)))
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), errFailSeek.Error()), err)
		err = appendFile.Close()
		require.NoError(t, err)
	})

	t.Run("check for sync", func(t *testing.T) {
		t.Parallel()
		c, file := createClientOnServer(t)
		require.NoError(t, c.Store("ok", file))
	})
}

func TestAPPEExistingFile(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	file := createTemporaryFile(t, 1*1024)
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err)

	defer func() { require.NoError(t, client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	_, err = file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	fileName := filepath.Base(file.Name())

	err = client.Store(fileName, file)
	require.NoError(t, err)

	_, err = file.Seek(0, io.SeekEnd)
	require.NoError(t, err)

	data := []byte("some more data")
	_, err = file.Write(data)
	require.NoError(t, err)

	_, err = file.Seek(1024, io.SeekStart)
	require.NoError(t, err)

	ftpUploadWithRawConnection(t, raw, file, fileName, true)

	info, err := client.Stat(fileName)
	require.NoError(t, err)
	require.Equal(t, int64(1024+len(data)), info.Size())

	localHash := hashFile(t, file)
	remoteHash := ftpDownloadAndHash(t, client, fileName)
	require.Equal(t, localHash, remoteHash)
}

func TestAPPENewFile(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	file := createTemporaryFile(t, 1*1024)
	_, err := file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err)

	defer func() { require.NoError(t, client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	fileName := filepath.Base(file.Name())

	ftpUploadWithRawConnection(t, raw, file, fileName, true)

	localHash := hashFile(t, file)
	remoteHash := ftpDownloadAndHash(t, client, fileName)
	require.Equal(t, localHash, remoteHash)
}

func TestTransfersFromOffset(t *testing.T) {
	driver := &TestServerDriver{
		Debug: false,
	}
	server := NewTestServerWithTestDriver(t, driver)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	file := createTemporaryFile(t, 1*1024)
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err)

	defer func() { require.NoError(t, client.Close()) }()

	_, err = file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	err = client.Store("file", file)
	require.NoError(t, err)

	_, err = file.Seek(0, io.SeekEnd)
	require.NoError(t, err)

	data := []byte("some more data")
	_, err = file.Write(data)
	require.NoError(t, err)

	_, err = file.Seek(1024, io.SeekStart)
	require.NoError(t, err)

	_, err = client.TransferFromOffset("file", nil, file, 1024)
	require.NoError(t, err)

	info, err := client.Stat("file")
	require.NoError(t, err)
	require.Equal(t, int64(1024+len(data)), info.Size())

	localHash := hashFile(t, file)
	remoteHash := ftpDownloadAndHash(t, client, "file")
	require.Equal(t, localHash, remoteHash)

	// finally test a partial RETR
	buf := bytes.NewBuffer(nil)
	_, err = client.TransferFromOffset("file", buf, nil, 1024)
	require.NoError(t, err)
	require.Equal(t, string(data), buf.String())
}

func TestBasicABOR(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("EPSV")
	require.NoError(t, err)
	require.Equal(t, StatusEnteringEPSV, returnCode)

	returnCode, _, err = raw.SendCommand(getABORCmd())
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode)

	// verify we are in sync
	returnCode, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	returnCode, _, err = raw.SendCommand("NLST")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, returnCode)

	returnCode, _, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode)

	// test ABOR cmd without special attention chars
	returnCode, _, err = raw.SendCommand("ABOR")
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode)

	// verify we are in sync
	returnCode, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
}

func TestTransferABOR(t *testing.T) {
	t.Run("passive-mode", func(t *testing.T) {
		server := NewTestServer(t, false)
		server.settings.PassiveTransferPortRange = &PortRange{
			Start: 49152,
			End:   65535,
		}
		conf := goftp.Config{
			User:     authUser,
			Password: authPass,
		}
		client, err := goftp.DialConfig(conf, server.Addr())
		require.NoError(t, err, "Couldn't connect")

		defer func() { require.NoError(t, client.Close()) }()

		aborTransfer(t, client)
	})

	t.Run("active-mode", func(t *testing.T) {
		server := NewTestServer(t, false)
		conf := goftp.Config{
			User:            authUser,
			Password:        authPass,
			ActiveTransfers: true,
		}
		server.settings.ActiveTransferPortNon20 = true
		client, err := goftp.DialConfig(conf, server.Addr())
		require.NoError(t, err, "Couldn't connect")

		defer func() { require.NoError(t, client.Close()) }()

		aborTransfer(t, client)
	})
}

func TestABORWithoutOpenTransfer(t *testing.T) {
	server := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, client.Close()) }()

	file := createTemporaryFile(t, 1*1024)
	err = client.Store("file.bin", file)
	require.NoError(t, err)

	err = client.Rename("file.bin", "delay-io-fail-to-seek.bin")
	require.NoError(t, err)

	_, err = client.Mkdir("delay-io-fail-to-readdir")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	_, err = file.Seek(1, io.SeekStart)
	require.NoError(t, err)

	returnCode, response, err := raw.SendCommand("REST 1")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionPending, returnCode, response)

	for _, cmd := range []string{
		"RETR delay-io-fail-to-seek.bin", "LIST delay-io-fail-to-readdir",
		"NLST delay-io-fail-to-readdir", "MLSD delay-io-fail-to-readdir",
	} {
		_, err = raw.PrepareDataConn()
		require.NoError(t, err)

		err = raw.SendCommandNoWaitResponse(cmd)
		require.NoError(t, err)

		returnCode, response, err = raw.SendCommand(getABORCmd())
		require.NoError(t, err)
		require.Equal(t, StatusClosingDataConn, returnCode, response)
		require.Equal(t, "ABOR successful; closing transfer connection", response)

		// verify we are in sync
		returnCode, _, err = raw.SendCommand("NOOP")
		require.NoError(t, err)
		require.Equal(t, StatusOK, returnCode)
	}

	returnCode, _, err = raw.SendCommand("QUIT")
	require.NoError(t, err)
	require.Equal(t, StatusClosingControlConn, returnCode)
}

func TestABORBeforeOpenTransfer(t *testing.T) {
	t.Run("passive-mode", func(t *testing.T) {
		server := NewTestServer(t, false)
		conf := goftp.Config{
			User:     authUser,
			Password: authPass,
		}
		server.settings.ActiveTransferPortNon20 = true
		client, err := goftp.DialConfig(conf, server.Addr())
		require.NoError(t, err, "Couldn't connect")

		defer func() { require.NoError(t, client.Close()) }()

		aborBeforeOpenTransfer(t, client)
	})

	t.Run("active-mode", func(t *testing.T) {
		server := NewTestServer(t, false)
		conf := goftp.Config{
			User:            authUser,
			Password:        authPass,
			ActiveTransfers: true,
		}
		server.settings.ActiveTransferPortNon20 = true
		client, err := goftp.DialConfig(conf, server.Addr())
		require.NoError(t, err, "Couldn't connect")

		defer func() { require.NoError(t, client.Close()) }()

		aborBeforeOpenTransfer(t, client)
	})
}

func aborTransfer(t *testing.T, client *goftp.Client) {
	t.Helper()

	file := createTemporaryFile(t, 1*1024)
	err := client.Store("file.bin", file)
	require.NoError(t, err)

	err = client.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	_, err = file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	returnCode, response, err := raw.SendCommand("RETR delay-io.bin")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatusOK, returnCode, response)
	require.Equal(t, "Using transfer connection", response)

	returnCode, response, err = raw.SendCommand("STAT")
	require.NoError(t, err)
	require.Equal(t, StatusSystemStatus, returnCode, response)
	require.Contains(t, response, "RETR delay-io.bin")
	require.NotContains(t, response, "Using transfer connection")
	require.NotContains(t, response, "Closing transfer connection")

	returnCode, response, err = raw.SendCommand(getABORCmd())
	require.NoError(t, err)
	require.Equal(t, StatusTransferAborted, returnCode, response)
	require.Equal(t, "Connection closed; transfer aborted", response)

	returnCode, response, err = raw.ReadResponse()
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode, response)
	require.Equal(t, "ABOR successful; closing transfer connection", response)

	// verify we are in sync
	returnCode, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
}

func aborBeforeOpenTransfer(t *testing.T, client *goftp.Client) {
	t.Helper()

	file := createTemporaryFile(t, 1*1024)
	err := client.Store("file.bin", file)
	require.NoError(t, err)

	err = client.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	_, err = file.Seek(1, io.SeekStart)
	require.NoError(t, err)

	returnCode, response, err := raw.SendCommand("REST 1")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionPending, returnCode, response)

	_, err = raw.PrepareDataConn()
	require.NoError(t, err)

	err = raw.SendCommandNoWaitResponse("RETR delay-io.bin")
	require.NoError(t, err)

	returnCode, response, err = raw.SendCommand(getABORCmd())
	require.NoError(t, err)
	require.Equal(t, StatusClosingDataConn, returnCode, response)
	require.Equal(t, "ABOR successful; closing transfer connection", response)

	// verify we are in sync
	returnCode, _, err = raw.SendCommand("NOOP")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
}

func TestASCIITransfers(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	file, err := os.CreateTemp("", "ftpserver")
	require.NoError(t, err)

	contents := []byte("line1\r\n\r\nline3\r\n,line4")
	_, err = file.Write(contents)
	require.NoError(t, err)

	defer func() { require.NoError(t, file.Close()) }()

	rc, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	_, err = file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	ftpUploadWithRawConnection(t, raw, file, "file.txt", false)

	files, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, files, 1)

	if runtime.GOOS != "windows" {
		require.Equal(t, int64(len(contents)-3), files[0].Size())
	} else {
		require.Equal(t, int64(len(contents)), files[0].Size())
	}

	remoteHash := ftpDownloadAndHashWithRawConnection(t, raw, "file.txt")
	localHash := hashFile(t, file)
	require.Equal(t, localHash, remoteHash)
}

func TestASCIITransfersInvalidFiles(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	file, err := os.CreateTemp("", "ftpserver")
	require.NoError(t, err)

	defer func() { require.NoError(t, file.Close()) }()

	buf := make([]byte, 1024*1024)
	for j := range buf {
		buf[j] = 65
	}

	_, err = file.Write(buf)
	require.NoError(t, err)

	localHash := hashFile(t, file)

	rc, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	ftpUploadWithRawConnection(t, raw, file, "file.bin", false)

	remoteHash := ftpDownloadAndHashWithRawConnection(t, raw, "file.bin")
	require.Equal(t, localHash, remoteHash)
}

func TestPASVWrappedListenerError(t *testing.T) {
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug:              true,
		errPassiveListener: os.ErrClosed,
	})
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, client.Close()) }()

	_, err = client.ReadDir("/")
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "421-Could not listen for passive connection")
	}
}

func TestPASVPublicIPResolver(t *testing.T) {
	server := NewTestServer(t, false)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { require.NoError(t, client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	server.settings.PublicHost = ""
	server.settings.PublicIPResolver = func(_ ClientContext) (string, error) {
		return "127.0.0", nil
	}
	// we crash if the PublicIPResolver returns an invalid IP, this must be fixed outside the lib
	returnCode, resp, err := raw.SendCommand("PASV")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, returnCode)
	require.Contains(t, resp, "invalid passive IP")

	server.settings.PublicIPResolver = func(_ ClientContext) (string, error) {
		return "", errConnectionNotAllowed
	}

	returnCode, resp, err = raw.SendCommand("PASV")
	require.NoError(t, err)
	require.Equal(t, StatusServiceNotAvailable, returnCode)
	require.Contains(t, resp, "couldn't fetch public IP")
}

func TestPASVConnectionWait(t *testing.T) {
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	require.NoError(t, err)

	tcpListener, err := net.ListenTCP("tcp", addr)
	require.NoError(t, err)

	cltHandler := clientHandler{
		conn: &testNetConn{
			remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 21},
		},
		server: &FtpServer{
			settings: &Settings{
				PasvConnectionsCheck:   IPMatchRequired,
				ActiveConnectionsCheck: IPMatchRequired,
			},
		},
	}

	transferHandler := passiveTransferHandler{ //nolint:forcetypeassert
		listener: &testNetListener{
			conn: &testNetConn{
				remoteAddr: &net.TCPAddr{IP: nil, Port: 21}, // invalid IP
			},
		},
		tcpListener:   tcpListener,
		Port:          tcpListener.Addr().(*net.TCPAddr).Port,
		settings:      cltHandler.server.settings,
		logger:        lognoop.NewNoOpLogger(),
		checkDataConn: cltHandler.checkDataConnectionRequirement,
	}

	defer func() {
		err = transferHandler.Close()
		assert.NoError(t, err)
	}()

	_, err = transferHandler.ConnectionWait(1 * time.Second)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid remote IP")
	}

	transferHandler.listener = &testNetListener{
		conn: &testNetConn{
			remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 21},
		},
	}

	_, err = transferHandler.ConnectionWait(1 * time.Second)
	assert.NoError(t, err)
}

// On Mac Os X, this requires to issue the following command:
// sudo ifconfig lo0 alias 127.0.1.1 up
func TestPASVIPMatch(t *testing.T) {
	server := NewTestServer(t, false)

	conn, err := net.DialTimeout("tcp", server.Addr(), 5*time.Second)
	require.NoError(t, err)

	defer func() {
		err = conn.Close()
		require.NoError(t, err)
	}()

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	resp := string(buf[:n])
	require.Equal(t, "220 TEST Server\r\n", resp)

	loginConnection(t, conn)

	for _, mode := range []DataConnectionRequirement{IPMatchRequired, IPMatchDisabled} {
		server.settings.PasvConnectionsCheck = mode

		_, err = conn.Write([]byte("PASV\r\n"))
		require.NoError(t, err)

		readBytes, err := conn.Read(buf)
		require.NoError(t, err)

		resp := string(buf[:readBytes])
		port := getPortFromPASVResponse(t, resp)
		assert.NotEqual(t, 0, port)

		_, err = conn.Write([]byte("LIST\r\n"))
		require.NoError(t, err)

		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		// now dial from 127.0.1.1 instead of 127.0.0.1
		dialer := net.Dialer{
			LocalAddr: &net.TCPAddr{
				IP:   net.ParseIP("127.0.1.1"),
				Port: 0,
			},
			Timeout: 5 * time.Second,
		}
		dataConn, err := dialer.Dial("tcp", addr)
		require.NoError(t, err)

		defer func() {
			err = dataConn.Close()
			assert.NoError(t, err)
		}()

		readBytes, err = conn.Read(buf)
		require.NoError(t, err)

		resp = string(buf[:readBytes])

		if mode == IPMatchRequired {
			require.Equal(t, "425 data connection security requirements not met", strings.TrimSpace(resp))
		} else {
			require.True(t, strings.HasPrefix(resp, "150 Using transfer connection"))
		}
	}
}

func TestPassivePortExhaustion(t *testing.T) {
	server := NewTestServer(t, false)
	server.settings.PassiveTransferPortRange = &PortRange{
		Start: 40000,
		End:   40005,
	}

	client, err := goftp.DialConfig(goftp.Config{
		User:     authUser,
		Password: authPass,
	}, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	for i := 0; i < 20; i++ {
		rc, message, err := raw.SendCommand("PASV")
		require.NoError(t, err)
		require.Equal(t, StatusEnteringPASV, rc, message)
	}
}

func loginConnection(t *testing.T, conn net.Conn) {
	t.Helper()

	buf := make([]byte, 1024)
	_, err := fmt.Fprintf(conn, "USER %v\r\n", authUser)
	require.NoError(t, err)

	readBytes, err := conn.Read(buf)
	require.NoError(t, err)

	resp := string(buf[:readBytes])
	require.True(t, strings.HasPrefix(resp, "331"))

	_, err = fmt.Fprintf(conn, "PASS %v\r\n", authPass)
	require.NoError(t, err)

	readBytes, err = conn.Read(buf)
	require.NoError(t, err)

	resp = string(buf[:readBytes])
	require.True(t, strings.HasPrefix(resp, "230"))
}

func getPortFromPASVResponse(t *testing.T, resp string) int {
	t.Helper()

	port := 0
	resp = strings.Replace(resp, "227 Entering Passive Mode", "", 1)
	resp = strings.Replace(resp, "(", "", 1)
	resp = strings.Replace(resp, ")", "", 1)
	resp = strings.TrimSpace(resp)
	respParts := strings.Split(resp, ",")
	require.Len(t, respParts, 6)

	for i, part := range respParts[4:6] {
		portOctet, err := strconv.Atoi(part)
		require.NoError(t, err)

		port |= portOctet << (byte(1-i) * 8)
	}

	return port
}
