package ftpserver

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

func createTemporaryFile(t *testing.T, targetSize int) *os.File {
	var file *os.File

	var fileErr error

	if file, fileErr = ioutil.TempFile("", "ftpserver"); fileErr != nil {
		t.Fatal("Temporary creation error:", fileErr)
		return nil
	}

	// nolint: gosec
	src := rand.New(rand.NewSource(0))
	if _, err := io.CopyN(file, src, int64(targetSize)); err != nil {
		t.Fatal("Couldn't copy:", err)
		return nil
	}

	t.Cleanup(func() {
		if err := os.Remove(file.Name()); err != nil {
			t.Fatalf("Problem deleting file \"%s\": %v", file.Name(), err)
		}
	})

	return file
}

func hashFile(t *testing.T, file *os.File) string {
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal("Couldn't seek:", err)
	}

	hashser := sha256.New()

	if _, err := io.Copy(hashser, file); err != nil {
		t.Fatal("Couldn't hashUpload:", err)
	}

	hash := hex.EncodeToString(hashser.Sum(nil))

	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal("Couldn't seek:", err)
	}

	return hash
}

func ftpUpload(t *testing.T, ftp *goftp.Client, file io.ReadSeeker, filename string) {
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
	hasher := sha256.New()
	if err := ftp.Retrieve(filename, hasher); err != nil {
		t.Fatal("Couldn't fetch file:", err)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func ftpDelete(t *testing.T, ftp *goftp.Client, filename string) {
	if err := ftp.Delete(filename); err != nil {
		t.Fatal("Couldn't delete file "+filename+":", err)
	}

	if err := ftp.Delete(filename); err == nil {
		t.Fatal("Should have had a problem deleting " + filename)
	}
}

func TestTransferIPv6(t *testing.T) {
	s := NewTestServerWithDriver(
		t,
		&TestServerDriver{
			Debug: true,
			Settings: &Settings{
				ActiveTransferPortNon20: true,
				ListenAddr:              "[::1]:0",
			},
		},
	)

	t.Run("active", func(t *testing.T) { testTransferOnConnection(t, s, true, false, false) })
	t.Run("passive", func(t *testing.T) { testTransferOnConnection(t, s, false, false, false) })
}

// TestTransfer validates the upload of file in both active and passive mode
func TestTransfer(t *testing.T) {
	t.Run("without-tls", func(t *testing.T) {
		s := NewTestServerWithDriver(
			t,
			&TestServerDriver{
				Debug: true,
				Settings: &Settings{
					ActiveTransferPortNon20: true,
				},
			},
		)

		testTransferOnConnection(t, s, false, false, false)
		testTransferOnConnection(t, s, true, false, false)
	})
	t.Run("with-tls", func(t *testing.T) {
		s := NewTestServerWithDriver(
			t,
			&TestServerDriver{
				Debug: true,
				TLS:   true,
				Settings: &Settings{
					ActiveTransferPortNon20: true,
				},
			},
		)

		testTransferOnConnection(t, s, false, true, false)
		testTransferOnConnection(t, s, true, true, false)
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		s := NewTestServerWithDriver(t, &TestServerDriver{
			Debug: true,
			TLS:   true,
			Settings: &Settings{
				ActiveTransferPortNon20: true,
				TLSRequired:             ImplicitEncryption,
			}})

		testTransferOnConnection(t, s, false, true, true)
		testTransferOnConnection(t, s, true, true, true)
	})
}

func testTransferOnConnection(t *testing.T, server *FtpServer, active, enableTLS, implicitTLS bool) {
	conf := goftp.Config{
		User:            authUser,
		Password:        authPass,
		ActiveTransfers: active,
	}
	if enableTLS {
		conf.TLSConfig = &tls.Config{
			// nolint:gosec
			InsecureSkipVerify: true,
		}
		if implicitTLS {
			conf.TLSMode = goftp.TLSImplicit
		} else {
			conf.TLSMode = goftp.TLSExplicit
		}
	}

	var err error
	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, server.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	var hashUpload, hashDownload string
	{ // We create a 10MB file and upload it
		file := createTemporaryFile(t, 10*1024*1024)
		hashUpload = hashFile(t, file)
		ftpUpload(t, c, file, "file.bin")
	}

	{ // We download the file we just uploaded
		hashDownload = ftpDownloadAndHash(t, c, "file.bin")
		ftpDelete(t, c, "file.bin")
	}

	// We make sure the hashes of the two files match
	if hashUpload != hashDownload {
		t.Fatal("The two files don't have the same hash:", hashUpload, "!=", hashDownload)
	}
}

func TestActiveModeDisabled(t *testing.T) {
	server := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
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

	var err error
	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, server.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	file := createTemporaryFile(t, 10*1024)
	err = c.Store("file.bin", file)

	if err == nil {
		t.Fatal("active mode is disabled, upload must fail")
	}

	if !strings.Contains(err.Error(), "421-PORT command is disabled") {
		t.Fatal("unexpected error", err)
	}
}

// TestFailedTransfer validates the handling of failed transfer caused by file access issues
func TestFailedTransfer(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error

	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, s.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	// We create a 1KB file and upload it
	file := createTemporaryFile(t, 1*1024)
	if err = c.Store("/non/existing/path/file.bin", file); err == nil {
		t.Fatal("This upload should have failed")
	}

	if err = c.Store("file.bin", file); err != nil {
		t.Fatal("This upload should have succeeded", err)
	}
}

func TestBogusTransferStart(t *testing.T) {
	s := NewTestServer(t, true)

	c, err := goftp.DialConfig(goftp.Config{User: "test", Password: "test"}, s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	rc, err := c.OpenRawConn()
	if err != nil {
		t.Fatal(err)
	}

	{ // Completely bogus port declaration
		status, resp, err := rc.SendCommand("PORT something")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusSyntaxErrorNotRecognised {
			t.Fatal("Bad status:", status, resp)
		}
	}

	{ // Completely bogus port declaration
		status, resp, err := rc.SendCommand("EPRT something")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusSyntaxErrorNotRecognised {
			t.Fatal("Bad status:", status, resp)
		}
	}

	{ // Bad port number: 0
		status, resp, err := rc.SendCommand("EPRT |2|::1|0|")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusSyntaxErrorNotRecognised {
			t.Fatal("Bad status:", status, resp)
		}
	}

	{ // Bad IP
		status, resp, err := rc.SendCommand("EPRT |1|253.254.255.256|2000|")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusSyntaxErrorNotRecognised {
			t.Fatal("Bad status:", status, resp)
		}
	}

	{ // Bad protocol type: 3
		status, resp, err := rc.SendCommand("EPRT |3|::1|2000|")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusSyntaxErrorNotRecognised {
			t.Fatal("Bad status:", status, resp)
		}
	}

	{ // We end-up on a positive note
		status, resp, err := rc.SendCommand("EPRT |1|::1|2000|")
		if err != nil {
			t.Fatal(err)
		}

		if status != StatusOK {
			t.Fatal("Bad status:", status, resp)
		}
	}
}

func TestFailedFileClose(t *testing.T) {
	driver := &TestServerDriver{
		Debug: true,
	}

	s := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	var err error

	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, s.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	file := createTemporaryFile(t, 1*1024)
	driver.FileOverride = &failingCloser{File: *file}
	err = c.Store("file.bin", file)

	if err == nil {
		t.Fatal("this upload should not succeed", err)
	}

	if !strings.Contains(err.Error(), errFailingCloser.Error()) {
		t.Errorf("got %s as the error message, want it to contain %s", err, errFailingCloser)
	}
}

type failingCloser struct {
	os.File
}

var errFailingCloser = errors.New("the hard disk crashed")

func (f *failingCloser) Close() error { return errFailingCloser }
