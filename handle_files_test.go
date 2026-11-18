package ftpserver

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validMLSxEntryPattern ensures an entry follows RFC3659 (section 7.2)
// https://tools.ietf.org/html/rfc3659#page-24
var validMLSxEntryPattern = regexp.MustCompile(`^ *(?:\w+=[^;]*;)* (.+)\r\n$`)

// exampleMLSTResponseEntry is taken from RFC3659 (section 7.7.2)
// https://tools.ietf.org/html/rfc3659#page-38
//
// C> PWD
// S> 257 "/" is current directory.
// C> MLst tmp
// S> 250- Listing tmp
// S>  Type=dir;Modify=19981107085215;Perm=el; /tmp
// S> 250 End
var exampleMLSTResponseEntry = " Type=dir;Modify=19981107085215;Perm=el; /tmp\r\n"

// exampleMLSDResponseEntry is taken from RFC3659 (section 7.7.3)
// https://tools.ietf.org/html/rfc3659#page-39
//
// C> MLSD tmp
// S> 150 BINARY connection open for MLSD tmp
// D> Type=cdir;Modify=19981107085215;Perm=el; tmp
// D> Type=cdir;Modify=19981107085215;Perm=el; /tmp
// D> Type=pdir;Modify=19990112030508;Perm=el; ..
// D> Type=file;Size=25730;Modify=19940728095854;Perm=; capmux.tar.z
// D> Type=file;Size=1830;Modify=19940916055648;Perm=r; hatch.c
// D> Type=file;Size=25624;Modify=19951003165342;Perm=r; MacIP-02.txt
// D> Type=file;Size=2154;Modify=19950501105033;Perm=r; uar.netbsd.patch
// D> Type=file;Size=54757;Modify=19951105101754;Perm=r; iptnnladev.1.0.sit.hqx
// D> Type=file;Size=226546;Modify=19970515023901;Perm=r; melbcs.tif
// D> Type=file;Size=12927;Modify=19961025135602;Perm=r; tardis.1.6.sit.hqx
// D> Type=file;Size=17867;Modify=19961025135602;Perm=r; timelord.1.4.sit.hqx
// D> Type=file;Size=224907;Modify=19980615100045;Perm=r; uar.1.2.3.sit.hqx
// D> Type=file;Size=1024990;Modify=19980130010322;Perm=r; cap60.pl198.tar.gz
// S> 226 MLSD completed
var exampleMLSDResponseEntries = []string{
	"Type=cdir;Modify=19981107085215;Perm=el; tmp \r\n",
	"Type=cdir;Modify=19981107085215;Perm=el; /tmp\r\n",
	"Type=pdir;Modify=19990112030508;Perm=el; ..\r\n",
	"Type=file;Size=25730;Modify=19940728095854;Perm=; capmux.tar.z\r\n",
	"Type=file;Size=1830;Modify=19940916055648;Perm=r; hatch.c\r\n",
	"Type=file;Size=25624;Modify=19951003165342;Perm=r; MacIP-02.txt\r\n",
	"Type=file;Size=2154;Modify=19950501105033;Perm=r; uar.netbsd.patch\r\n",
	"Type=file;Size=54757;Modify=19951105101754;Perm=r; iptnnladev.1.0.sit.hqx\r\n",
	"Type=file;Size=226546;Modify=19970515023901;Perm=r; melbcs.tif\r\n",
	"Type=file;Size=12927;Modify=19961025135602;Perm=r; tardis.1.6.sit.hqx\r\n",
	"Type=file;Size=17867;Modify=19961025135602;Perm=r; timelord.1.4.sit.hqx\r\n",
	"Type=file;Size=224907;Modify=19980615100045;Perm=r; uar.1.2.3.sit.hqx\r\n",
	"Type=file;Size=1024990;Modify=19980130010322;Perm=r; cap60.pl198.tar.gz\r\n",
}

func TestMLSxEntryValidation(t *testing.T) {
	expectedPathentry := "/tmp"
	actualPathentry := validMLSxEntryPattern.FindStringSubmatch(exampleMLSTResponseEntry)

	if len(actualPathentry) != 2 {
		t.Errorf("Valid MLST response example did not pass validation: \"%s\"", exampleMLSTResponseEntry)
	} else if actualPathentry[1] != expectedPathentry {
		t.Errorf("Validation returned incorrect pathentry: got \"%s\", want \"%s\"", actualPathentry, expectedPathentry)
	}

	for _, entry := range exampleMLSDResponseEntries {
		if !validMLSxEntryPattern.MatchString(entry) {
			t.Errorf("Valid MLSD response example did not pass validation: \"%s\"", entry)
		}
	}
}

func TestALLO(t *testing.T) {
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

	// Asking for too much (2MB)
	returnCode, _, err := raw.SendCommand("ALLO 2000000")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should have been refused")

	// Asking for the right amount of space (500KB)
	returnCode, _, err = raw.SendCommand("ALLO 500000")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")

	// Wrong size
	returnCode, _, err = raw.SendCommand("ALLO 500000a")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have been refused")
}

func TestCHMOD(t *testing.T) {
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

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("SITE CHMOD a file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should have been refused")

	returnCode, response, err := raw.SendCommand("SITE CHMOD 644")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "A missing path should be refused")
	require.NotContains(t, response, "internal error")

	returnCode, _, err = raw.SendCommand("SITE CHMOD 600 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")
}

func TestCHOWN(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Asking for a chown user change that isn't authorized
	returnCode, _, err := raw.SendCommand("SITE CHOWN 1001:500 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should have been refused")

	// Asking for a chown user change that isn't authorized
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1001 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should have been refused")

	// Asking for the right chown user
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000:500 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")

	// Asking for the right chown user
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")

	// Asking for a chown on a file that doesn't exist
	returnCode, _, err = raw.SendCommand("SITE CHOWN test file2")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should NOT have been accepted")

	// Asking for a chown with a missing parameter
	returnCode, _, err = raw.SendCommand("SITE CHOWN 1000")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should NOT have been accepted")
}

func TestMFMT(t *testing.T) {
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

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Good
	returnCode, _, err := raw.SendCommand("MFMT 20201209211059 file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode, "Should have succeeded")

	// 3 params instead of 2
	returnCode, _, err = raw.SendCommand("MFMT 20201209211059 file somethingelse")
	require.NoError(t, err)
	require.NotEqual(t, StatusFileStatus, returnCode, "Should have failed")

	// 1 param instead of 2
	returnCode, _, err = raw.SendCommand("MFMT 202012092110 file")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have failed")

	// no parameters
	returnCode, _, err = raw.SendCommand("MFMT")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode, "Should have failed")

	// Good (to make sure we are still in sync)
	returnCode, _, err = raw.SendCommand("MFMT 20201209211059 file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode, "Should have succeeded")
}

func TestSYMLINK(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Bad syntaxes
	returnCode, _, err := raw.SendCommand("SITE SYMLINK")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have been refused")

	returnCode, _, err = raw.SendCommand("SITE SYMLINK ")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have been refused")

	returnCode, _, err = raw.SendCommand("SITE SYMLINK file1")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have been refused")

	returnCode, _, err = raw.SendCommand("SITE SYMLINK file1 file2 file3")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, "Should have been refused")

	// Creating a bad symlink is authorized
	returnCode, _, err = raw.SendCommand("SITE SYMLINK file3 file4")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")

	// Overwriting a file is not authorized
	returnCode, _, err = raw.SendCommand("SITE SYMLINK file5 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, "Should have been refused")

	// disable SITE
	server.settings.DisableSite = true

	returnCode, _, err = raw.SendCommand("SITE SYMLINK file test")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, returnCode, "Should have been refused")

	server.settings.DisableSite = false

	// Good symlink
	returnCode, _, err = raw.SendCommand("SITE SYMLINK file test")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, "Should have been accepted")
}

func TestSTATFile(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	// Create a directory with a subdir
	_, err = client.Mkdir("dir")
	require.NoError(t, err)

	_, err = client.Mkdir("/dir/sub")
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("STAT file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)

	returnCode, message, err := raw.SendCommand("STAT dir")
	require.NoError(t, err)
	require.Equal(t, StatusDirectoryStatus, returnCode)
	require.Regexp(t, `(?m)^ d[-rwx]{9} 1 `+fakeUser+" "+fakeGroup+` +\d+ .+ sub$`, message, "each entry is a LIST line")

	// finally stat a missing file dir
	returnCode, _, err = raw.SendCommand("STAT missing")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, returnCode)

	// the test driver will fail to open this dir
	dirName, err := client.Mkdir("fail-to-open")
	require.NoError(t, err)

	returnCode, _, err = raw.SendCommand("STAT %v", dirName)
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, returnCode)
}

func TestMLST(t *testing.T) {
	req := require.New(t)
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, server.Addr())
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	req.NoError(err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, rsp, err := raw.SendCommand("MLST file")
	req.NoError(err)
	req.Equal(StatusFileOK, rc)

	lines := strings.Split(rsp, "\n")
	req.Len(lines, 3)
	path := validMLSxEntryPattern.FindStringSubmatch(lines[1] + "\r\n")

	if len(path) != 2 {
		t.Errorf("Valid MLST response example did not pass validation: \"%s\"", lines[1])
	} else if path[1] != "file" {
		t.Errorf("Validation returned incorrect pathentry: got \"%s\", want \"%s\"", path, "file")
	}
}

func TestMDTM(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Creating a tiny file
	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, _, err := raw.SendCommand("MDTM file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)

	returnCode, _, err = raw.SendCommand("MDTM missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode)
}

func TestRename(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	ftpUpload(t, client, createTemporaryFile(t, 10), "file")

	err = client.Rename("file", "file1")
	require.NoError(t, err)

	// the test driver returns FileNameNotAllowedError in this case, the error code should be 553 instead of 550
	err = client.Rename("file1", "not-allowed")
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "553-Couldn't rename", err.Error())
	}

	// renaming a missing file must fail
	err = client.Rename("missingfile", "file1")
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "550-Couldn't access", err.Error())
	}

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("RNTO file2")
	require.NoError(t, err)
	require.Equal(t, StatusBadCommandSequence, rc)
}

func TestUploadErrorCodes(t *testing.T) {
	s := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	client, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	tempFile := createTemporaryFile(t, 10)
	_, err = tempFile.Seek(0, 0)
	require.NoError(t, err, "Couldn't seek")
	err = client.Store("quota-exceeded", tempFile)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "552-Could not access file")
	}

	_, err = tempFile.Seek(0, 0)
	require.NoError(t, err, "Couldn't seek")
	err = client.Store("not-allowed", tempFile)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "553-Could not access file")
	}
}

func requireFTPError(t *testing.T, err error, code int, message string) {
	t.Helper()

	var ftpErr goftp.Error

	require.ErrorAs(t, err, &ftpErr)
	require.Equal(t, code, ftpErr.Code(), ftpErr.Message())
	require.Equal(t, message, ftpErr.Message())
}

func TestDriverErrorReplies(t *testing.T) {
	t.Parallel()

	req := require.New(t)
	driver := &TestServerDriver{
		Settings: &Settings{EnableCOMB: true, EnableHASH: true},
	}
	server := NewTestServerWithTestDriver(t, driver)

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	req.NoError(err)

	defer func() { panicOnError(client.Close()) }()

	err = client.Store("quota-exceeded.bin", createTemporaryFile(t, 10))
	requireFTPError(t, err, StatusActionAborted, "Could not access file: storage limit exceeded")

	ftpUpload(t, client, createTemporaryFile(t, 10), "file.bin")
	req.NoError(client.Rename("file.bin", "fail-to-open.bin"))

	err = client.Retrieve("fail-to-open.bin", io.Discard)
	requireFTPError(t, err, StatusActionNotTaken, "Could not access file: "+errFailOpen.Error())

	raw, err := client.OpenRawConn()
	req.NoError(err)

	defer func() { req.NoError(raw.Close()) }()

	replies := []struct {
		command string
		code    int
		message string
	}{
		{"XMD5 fail-to-open.bin", StatusActionNotTaken, "fail-to-open.bin: " + errFailOpen.Error()},
		{"COMB quota-exceeded.bin fail-to-open.bin", StatusActionAborted,
			`Could not access file "/quota-exceeded.bin": storage limit exceeded`},
		{"COMB combined.bin fail-to-open.bin", StatusActionNotTaken,
			`Could not access file "/fail-to-open.bin": ` + errFailOpen.Error()},
	}

	for _, reply := range replies {
		returnCode, message, errCmd := raw.SendCommand("%s", reply.command)
		req.NoError(errCmd, reply.command)
		req.Equal(reply.code, returnCode, "%s: %s", reply.command, message)
		req.Equal(reply.message, message, reply.command)
	}

	_, statErr := driver.fs.Stat("/missing")
	req.Error(statErr)

	returnCode, message, err := raw.SendCommand("LIST /missing")
	req.NoError(err)
	req.Equal(StatusFileActionNotTaken, returnCode, message)
	req.Equal("Could not list: "+statErr.Error(), message)
}

func TestDriverTransferErrorReplies(t *testing.T) {
	t.Parallel()

	server := NewTestServer(t, false)

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	require.NoError(t, err)

	defer func() { panicOnError(client.Close()) }()

	uploads := []struct {
		path    string
		code    int
		message string
	}{
		{"storage-full-on-write.bin", StatusActionAborted, "Issue during transfer: storage limit exceeded"},
		{"fail-to-write.bin", StatusActionNotTaken, "Issue during transfer: " + errFailWrite.Error()},
	}

	// An empty upload reaches the driver's Write after the copy, a non-empty one from inside it.
	// Store would retry a failed non-empty upload, TransferFromOffset doesn't.
	for _, size := range []int{0, 10} {
		for _, upload := range uploads {
			src := createTemporaryFile(t, size)
			_, err = src.Seek(0, io.SeekStart)
			require.NoError(t, err)

			_, err = client.TransferFromOffset(upload.path, nil, src, 0)
			requireFTPError(t, err, upload.code, upload.message)
		}
	}

	ftpUpload(t, client, createTemporaryFile(t, 10), "fail-on-read.bin")

	err = client.Retrieve("fail-on-read.bin", io.Discard)
	requireFTPError(t, err, StatusActionNotTaken, "Issue during transfer: "+errFailRead.Error())
}

// In ASCII mode the converter, not the data connection, is the source of an upload
func TestASCIIUploadDriverErrorReply(t *testing.T) {
	t.Parallel()

	server := NewTestServer(t, false)

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	require.NoError(t, err)

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	requireRawCommand(t, raw, "TYPE A", StatusOK)

	uploads := []struct {
		path    string
		code    int
		message string
	}{
		{"storage-full-on-write.txt", StatusActionAborted, "Issue during transfer: storage limit exceeded"},
		{"fail-to-write.txt", StatusActionNotTaken, "Issue during transfer: " + errFailWrite.Error()},
	}

	for _, upload := range uploads {
		dcGetter, err := raw.PrepareDataConn()
		require.NoError(t, err)

		requireRawCommand(t, raw, "STOR "+upload.path, StatusFileStatusOK)

		dataConn, err := dcGetter()
		require.NoError(t, err)

		_, err = dataConn.Write([]byte("line\r\n"))
		require.NoError(t, err)
		require.NoError(t, dataConn.Close())

		returnCode, response, err := raw.ReadResponse()
		require.NoError(t, err)
		require.Equal(t, upload.code, returnCode, response)
		require.Equal(t, upload.message, response)
	}
}

// A driver error that is a nil pointer must neither break the reply nor leave the REST offset to the next transfer
func TestTypedNilDriverErrorReply(t *testing.T) {
	t.Parallel()

	server := NewTestServer(t, false)

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	require.NoError(t, err)

	defer func() { panicOnError(client.Close()) }()

	file := createTemporaryFile(t, 10)
	ftpUpload(t, client, file, "file.bin")

	raw, err := client.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	requireRawCommand(t, raw, "TYPE I", StatusOK)
	requireRawCommand(t, raw, "REST 5", StatusFileActionPending)

	response := requireRawCommand(t, raw, "RETR typed-nil-error.bin", StatusActionNotTaken)
	require.Equal(t, "Could not access file: <nil>", response)

	require.Equal(t, hashFile(t, file), ftpDownloadAndHashWithRawConnection(t, raw, "file.bin", nil))
}

// tcpConnPair returns both ends of a loopback TCP connection: io.Copy reaches net.TCPConn's ReadFrom and
// WriteTo, which wrap the file's errors, only with a real one.
func tcpConnPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { require.NoError(t, listener.Close()) }()

	dialed, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
	require.NoError(t, err)

	accepted, err := listener.Accept()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = dialed.Close()
		_ = accepted.Close()
	})

	server, ok := accepted.(*net.TCPConn)
	require.True(t, ok)

	client, ok := dialed.(*net.TCPConn)
	require.True(t, ok)

	return server, client
}

func newTransferTestHandler(transferType TransferType, logs io.Writer) *clientHandler {
	return &clientHandler{
		server:              &FtpServer{settings: &Settings{}},
		logger:              slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		currentTransferType: transferType,
	}
}

// errors.Is panics on this type: it is comparable but holds an uncomparable value
type uncomparableValueError struct {
	cause any
}

func (e uncomparableValueError) Error() string {
	return fmt.Sprintf("invalid value: %v", e.cause)
}

// errors.Is never matches this type: it is uncomparable
type uncomparableError struct {
	fields []string
}

func (e uncomparableError) Error() string {
	return "invalid fields: " + strings.Join(e.fields, ", ")
}

// failingFile fails its reads and writes with err or, if shortWrite, writes half of the data without an error
type failingFile struct {
	err         error
	shortWrite  bool
	transferErr error
}

func (f *failingFile) Read([]byte) (int, error) {
	return 0, f.err
}

func (f *failingFile) Write(p []byte) (int, error) {
	if f.shortWrite {
		return len(p) / 2, nil
	}

	return 0, f.err
}

func (f *failingFile) TransferError(err error) {
	f.transferErr = err
}

// joiningConn is a data connection with its own ReadFrom, which joins another error to the file's
type joiningConn struct{}

func (joiningConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (joiningConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (joiningConn) ReadFrom(r io.Reader) (int64, error) {
	_, err := r.Read(make([]byte, 1))

	return 0, errors.Join(err, ErrStorageExceeded)
}

type fileErrorReplyCase struct {
	name      string
	conn      io.ReadWriter // a TCP connection if nil
	file      *failingFile
	upload    bool
	code      int
	driverErr error  // shown in the reply and given to TransferError
	copyErr   string // in the copy's full error, which is logged
}

// The reply shows the error the driver's file returned, and its code maps from the copy's full error
func TestTransferFileErrorReplies(t *testing.T) {
	cases := make([]fileErrorReplyCase, 0, 6)

	// errors.Is panics on, or never matches, these driver errors
	for _, driverErr := range []error{
		uncomparableValueError{cause: []string{"a", "b"}},
		uncomparableError{fields: []string{"a", "b"}},
	} {
		cases = append(cases,
			fileErrorReplyCase{name: fmt.Sprintf("%T download", driverErr), file: &failingFile{err: driverErr},
				code: StatusActionNotTaken, driverErr: driverErr, copyErr: "readfrom tcp"},
			fileErrorReplyCase{name: fmt.Sprintf("%T upload", driverErr), file: &failingFile{err: driverErr},
				upload: true, code: StatusActionNotTaken, driverErr: driverErr, copyErr: "writeto tcp"},
		)
	}

	cases = append(cases,
		fileErrorReplyCase{name: "short write", file: &failingFile{shortWrite: true}, upload: true,
			code: StatusActionNotTaken, driverErr: io.ErrShortWrite, copyErr: "writeto tcp"},
		fileErrorReplyCase{name: "file error joined by the connection", conn: joiningConn{},
			file: &failingFile{err: errFailRead}, code: StatusActionAborted, driverErr: errFailRead,
			copyErr: ErrStorageExceeded.Error()},
	)

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			conn := testCase.conn
			if conn == nil {
				serverConn, clientConn := tcpConnPair(t)
				if testCase.upload {
					_, err := clientConn.Write([]byte("data"))
					require.NoError(t, err)
				}

				conn = serverConn
			}

			logs := &bytes.Buffer{}
			reply := &bytes.Buffer{}
			handler := newTransferTestHandler(TransferTypeBinary, logs)
			handler.writer = bufio.NewWriter(reply)

			require.NotPanics(t, func() {
				handler.TransferClose(nil, handler.doFileTransfer(conn, testCase.file, testCase.upload))
			})

			require.Equal(t, fmt.Sprintf("%d Issue during transfer: %v\r\n", testCase.code, testCase.driverErr),
				reply.String())
			require.Equal(t, testCase.driverErr, testCase.file.transferErr)
			require.Contains(t, logs.String(), testCase.copyErr)
		})
	}
}

// partialReadFile returns data along with its error, as io.Reader allows
type partialReadFile struct {
	err error
}

func (f partialReadFile) Read(p []byte) (int, error) {
	return copy(p, "data"), f.err
}

func (partialReadFile) Write(p []byte) (int, error) {
	return len(p), nil
}

// When writing the data the file returned along with its error fails, the connection's error is kept.
func TestTransferConnectionErrorKept(t *testing.T) {
	serverConn, _ := tcpConnPair(t)
	require.NoError(t, serverConn.SetWriteDeadline(time.Now().Add(-time.Second)))

	handler := newTransferTestHandler(TransferTypeBinary, io.Discard)
	err := handler.doFileTransfer(serverConn, partialReadFile{err: os.ErrDeadlineExceeded}, false)

	var opErr *net.OpError

	require.ErrorAs(t, err, &opErr)
	require.Equal(t, "readfrom", opErr.Op)
}

// recordingOSFile counts the calls to the copy paths of its *os.File
type recordingOSFile struct {
	*os.File
	readFromCalls int
	writeToCalls  int
}

func (f *recordingOSFile) ReadFrom(r io.Reader) (int64, error) {
	f.readFromCalls++

	return f.File.ReadFrom(r)
}

func (f *recordingOSFile) WriteTo(w io.Writer) (int64, error) {
	f.writeToCalls++

	return f.File.WriteTo(w)
}

// syscallConnOnlyFile is a file whose only copy path is the SyscallConn of its *os.File
type syscallConnOnlyFile struct {
	file             *os.File
	syscallConnCalls int
}

func (f *syscallConnOnlyFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *syscallConnOnlyFile) Write(p []byte) (int, error) {
	return f.file.Write(p)
}

func (f *syscallConnOnlyFile) SyscallConn() (syscall.RawConn, error) {
	f.syscallConnCalls++

	return f.file.SyscallConn()
}

// Transfers hand the file to the copy as is where wrapping it would hide its zero-copy path or its own ReadFrom
func TestTransferKeepsFileCopyPaths(t *testing.T) {
	data := strings.Repeat("line\r\n", 1000)

	asciiStored := strings.Repeat("line\n", 1000)
	if runtime.GOOS == "windows" {
		asciiStored = data
	}

	readFrom := func(osFile *os.File) (io.ReadWriter, func() int) {
		file := &recordingOSFile{File: osFile}

		return file, func() int { return file.readFromCalls }
	}

	cases := []struct {
		name         string
		transferType TransferType
		upload       bool
		stored       string // what an upload writes to the file
		linuxOnly    bool
		wrap         func(osFile *os.File) (io.ReadWriter, func() int)
	}{
		{name: "binary upload with ReadFrom", transferType: TransferTypeBinary, upload: true, stored: data,
			wrap: readFrom},
		{name: "ASCII upload with ReadFrom", transferType: TransferTypeASCII, upload: true, stored: asciiStored,
			wrap: readFrom},
		{name: "download with WriteTo", transferType: TransferTypeBinary,
			wrap: func(osFile *os.File) (io.ReadWriter, func() int) {
				file := &recordingOSFile{File: osFile}

				return file, func() int { return file.writeToCalls }
			}},
		{name: "download with SyscallConn", transferType: TransferTypeBinary, linuxOnly: true,
			wrap: func(osFile *os.File) (io.ReadWriter, func() int) {
				file := &syscallConnOnlyFile{file: osFile}

				return file, func() int { return file.syscallConnCalls }
			}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.linuxOnly && runtime.GOOS != "linux" {
				t.Skip("only Linux's sendfile goes through SyscallConn")
			}

			osFile, err := os.CreateTemp(t.TempDir(), "file")
			require.NoError(t, err)

			defer func() { require.NoError(t, osFile.Close()) }()

			serverConn, clientConn := tcpConnPair(t)

			// a download fits in the client's socket buffer
			if testCase.upload {
				_, err = clientConn.Write([]byte(data))
				require.NoError(t, err)
				require.NoError(t, clientConn.Close())
			} else {
				_, err = osFile.WriteString(data)
				require.NoError(t, err)
				_, err = osFile.Seek(0, io.SeekStart)
				require.NoError(t, err)
			}

			file, calls := testCase.wrap(osFile)
			handler := newTransferTestHandler(testCase.transferType, io.Discard)
			require.NoError(t, handler.doFileTransfer(serverConn, file, testCase.upload))
			require.Equal(t, 1, calls())

			if testCase.upload {
				stored, errRead := os.ReadFile(osFile.Name())
				require.NoError(t, errRead)
				require.Equal(t, testCase.stored, string(stored))
			}
		})
	}
}

type syscallConnFile struct {
	afero.File
}

func (syscallConnFile) SyscallConn() (syscall.RawConn, error) {
	return nil, errors.ErrUnsupported
}

// Transfers wrap the file to mark its errors only where that can't hide a zero-copy path.
func TestCanZeroCopy(t *testing.T) {
	osFile := createTemporaryFile(t, 0)

	cases := []struct {
		name     string
		file     io.ReadWriter
		upload   bool
		download bool
	}{
		{"os.File", osFile, true, true},
		{"afero.File wrapper", &testFile{File: osFile}, false, false},
		{"SyscallConn without WriteTo", syscallConnFile{File: osFile}, false, true},
	}

	for _, testCase := range cases {
		require.Equal(t, testCase.upload, canZeroCopy(testCase.file, true), testCase.name)
		require.Equal(t, testCase.download, canZeroCopy(testCase.file, false), testCase.name)
	}
}

func TestHASHDisabled(t *testing.T) {
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

	rc, message, err := raw.SendCommand("XSHA256 file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, rc, message)
}

func TestHASHCommand(t *testing.T) {
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

	dir, err := client.Mkdir("testdir")
	require.NoError(t, err)

	tempFile, err := os.CreateTemp("", "ftpserver")
	require.NoError(t, err)
	err = os.WriteFile(tempFile.Name(), []byte("sample data with know checksum/hash\n"), 0o600)
	require.NoError(t, err)

	crc32Sum := "21b0f382"
	sha256Hash := "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b"

	ftpUpload(t, client, tempFile, "file.txt")
	ftpUpload(t, client, tempFile, "file with space.txt")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// ask hash for a directory
	returnCode, _, err := raw.SendCommand("XSHA256 %v", dir)
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTakenNoFile, returnCode)

	// test the HASH command
	returnCode, message, err := raw.SendCommand("HASH file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("SHA-256 0-36 %v file.txt", sha256Hash)))
	// test the same quoting the file name
	returnCode, message, err = raw.SendCommand(`HASH "file with space.txt"`)
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("SHA-256 0-36 %v file with space.txt", sha256Hash)))

	// change algo and request the hash again
	returnCode, message, err = raw.SendCommand("OPTS HASH CRC32")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode)
	require.Equal(t, "CRC32", message)

	returnCode, message, err = raw.SendCommand("HASH file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("CRC32 0-36 %v file.txt", crc32Sum)))
}

func TestHashStatsAbsolutePath(t *testing.T) {
	t.Parallel()

	req := require.New(t)
	server, recorder := newStatRecordingServer(t, &Settings{EnableHASH: true})

	client, err := goftp.DialConfig(goftp.Config{User: authUser, Password: authPass}, server.Addr())
	req.NoError(err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	_, err = client.Mkdir("/sub")
	req.NoError(err)
	_, err = client.Mkdir("/sub/deep")
	req.NoError(err)

	tempFile, err := os.CreateTemp(t.TempDir(), "ftpserver")
	req.NoError(err)
	req.NoError(os.WriteFile(tempFile.Name(), []byte("sample data with know checksum/hash\n"), 0o600))
	ftpUpload(t, client, tempFile, "/sub/deep/file.txt")

	raw, err := client.OpenRawConn()
	req.NoError(err, "Couldn't open raw connection")

	defer func() { req.NoError(raw.Close()) }()

	returnCode, _, err := raw.SendCommand("CWD /sub/deep")
	req.NoError(err)
	req.Equal(StatusFileOK, returnCode)

	recorder.statPaths()

	// A path escaping upwards reaches Stat cleaned and absolute
	_, _, err = raw.SendCommand("XSHA256 ../../x")
	req.NoError(err)
	req.Equal([]string{"/x"}, recorder.statPaths())

	// A relative name is resolved against the current directory, and still hashes the right file
	returnCode, message, err := raw.SendCommand("XSHA256 file.txt")
	req.NoError(err)
	req.Equal(StatusFileOK, returnCode)
	req.Contains(message, "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b")
	req.Equal([]string{"/sub/deep/file.txt"}, recorder.statPaths())
}

func TestHashWithoutParams(t *testing.T) {
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

	returnCode, message, err := raw.SendCommand("HASH")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)
	require.Contains(t, message, "invalid HASH parameters")
}

func TestCustomHASHCommands(t *testing.T) {
	server := NewTestServer(t, false)
	server.settings.EnableHASH = true
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	tempFile, err := os.CreateTemp("", "ftpserver")
	require.NoError(t, err)
	_, err = tempFile.WriteString("sample data with know checksum/hash\n")
	require.NoError(t, err)

	ftpUpload(t, client, tempFile, "file.txt")

	err = tempFile.Close()
	require.NoError(t, err)

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	hashMapping := getKnownHASHMappings()

	var returnCode int
	var message string

	for cmd, expected := range hashMapping {
		returnCode, message, err = raw.SendCommand("%v file.txt", cmd)
		require.NoError(t, err)
		require.Equal(t, StatusFileOK, returnCode)
		require.True(t, strings.HasSuffix(message, expected))
	}

	// now a partial hash
	returnCode, message, err = raw.SendCommand("XSHA256 file.txt 7 11")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode)
	require.True(t, strings.HasSuffix(message, "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7"))

	// invalid start
	returnCode, _, err = raw.SendCommand("XSHA256 file.txt a 11")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)

	// invalid end
	returnCode, _, err = raw.SendCommand("XSHA256 file.txt 7 a")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode)
}

func TestCOMB(t *testing.T) {
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

	returnCode, message, err := raw.SendCommand("COMB file.bin 1 2")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, returnCode, message)

	server.settings.EnableCOMB = true

	parts := make([]*os.File, 0, 4)

	partSize := 1024
	hasher := sha256.New()

	parts = append(parts, createTemporaryFile(t, partSize), createTemporaryFile(t, partSize),
		createTemporaryFile(t, partSize), createTemporaryFile(t, partSize))

	for idx, part := range parts {
		ftpUpload(t, client, part, strconv.Itoa(idx))
		_, err = part.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, err = io.Copy(hasher, part)
		require.NoError(t, err)
	}

	returnCode, message, err = raw.SendCommand("COMB file.bin 0 1 2 3")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, returnCode, message)
	require.Equal(t, "COMB succeeded!", message)

	info, err := client.Stat("file.bin")
	require.NoError(t, err)
	require.Equal(t, int64(partSize*4), info.Size())

	hashParts := hex.EncodeToString(hasher.Sum(nil))
	hashCombined := ftpDownloadAndHash(t, client, "file.bin")
	require.Equal(t, hashParts, hashCombined)

	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
}

func TestCOMBAppend(t *testing.T) {
	server := NewTestServerWithTestDriver(
		t,
		&TestServerDriver{
			Debug: false,
			Settings: &Settings{
				EnableCOMB: true,
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

	partSize := 1024
	hasher := sha256.New()

	initialFile := createTemporaryFile(t, partSize)
	ftpUpload(t, client, initialFile, "file.bin")

	_, err = initialFile.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = io.Copy(hasher, initialFile)
	require.NoError(t, err)

	parts := make([]*os.File, 0, 2)

	parts = append(parts, createTemporaryFile(t, partSize), createTemporaryFile(t, partSize))

	for idx, part := range parts {
		ftpUpload(t, client, part, fmt.Sprintf(" %d ", idx))
		_, err = part.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, err = io.Copy(hasher, part)
		require.NoError(t, err)
	}

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, message, err := raw.SendCommand("COMB file.bin \" 0 \" \" 1 \"")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc, message)
	require.Equal(t, "COMB succeeded!", message)

	info, err := client.Stat("file.bin")
	require.NoError(t, err)
	require.Equal(t, int64(partSize*3), info.Size())

	hashParts := hex.EncodeToString(hasher.Sum(nil))
	hashCombined := ftpDownloadAndHash(t, client, "file.bin")
	require.Equal(t, hashParts, hashCombined)

	contents, err := client.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
}

func TestCOMBRejectsRepeatedPaths(t *testing.T) {
	server := NewTestServerWithTestDriver(
		t,
		&TestServerDriver{
			Debug: false,
			Settings: &Settings{
				EnableCOMB: true,
			},
		},
	)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
		// Without the check, a COMB listing its target as a source never answers:
		// the timeout makes the test fail instead of hang.
		Timeout: 5 * time.Second,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	partSize := 1024
	ftpUpload(t, client, createTemporaryFile(t, partSize), "a")
	ftpUpload(t, client, createTemporaryFile(t, partSize), "b")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	for _, cmd := range []string{"COMB a a", "COMB a b a", "COMB t a ./a", "COMB /a ../a"} {
		returnCode, message, errCmd := raw.SendCommand("%s", cmd)
		require.NoError(t, errCmd, cmd)
		require.Equal(t, StatusSyntaxErrorParameters, returnCode, "%s: %s", cmd, message)

		info, errStat := client.Stat("a")
		require.NoError(t, errStat, cmd)
		require.Equal(t, int64(partSize), info.Size(), cmd)

		info, err = client.Stat("b")
		require.NoError(t, err, cmd)
		require.Equal(t, int64(partSize), info.Size(), cmd)
	}

	_, err = client.Stat("t")
	require.Error(t, err, "target of a rejected COMB must not be created")
}

func TestCOMBCloseError(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	server.settings.EnableCOMB = true

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	ftpUpload(t, client, createTemporaryFile(t, 10), "1.bin")
	ftpUpload(t, client, createTemporaryFile(t, 10), "2.bin")

	rc, message, err := raw.SendCommand("COMB fail-to-close.bin 1.bin 2.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, message)
	require.Contains(t, message, "Could not close combined file")
}

func TestREST(t *testing.T) {
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

	returnCode, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	returnCode, response, err = raw.SendCommand("REST 10")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, response)

	returnCode, response, err = raw.SendCommand("TYPE I")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	returnCode, response, err = raw.SendCommand("REST a")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasPrefix(response, "Couldn't parse size"))
}

func TestRESTInASCIIWithConversionDisabled(t *testing.T) {
	driver := &TestServerDriver{
		Settings: &Settings{DisableASCIIConversion: true},
	}
	server := NewTestServerWithTestDriver(t, driver)
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

	// With DisableASCIIConversion transfers are byte-for-byte even in ASCII
	// mode, so REST must be accepted.
	returnCode, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	returnCode, response, err = raw.SendCommand("REST 10")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionPending, returnCode, response)

	// Invalid argument is still rejected regardless of conversion setting.
	returnCode, response, err = raw.SendCommand("REST a")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasPrefix(response, "Couldn't parse size"))
}

func TestSIZE(t *testing.T) {
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

	testSizeBasicOperations(t, client, raw)
	testSizeWithDirectories(t, client, raw)
	testSizeInASCIIMode(t, raw)
}

func testSizeBasicOperations(t *testing.T, client *goftp.Client, raw goftp.RawConn) {
	t.Helper()
	returnCode, response, err := raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasPrefix(response, "Couldn't access"))

	ftpUpload(t, client, createTemporaryFile(t, 10), "file.bin")

	returnCode, response, err = raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode, response)
	require.Equal(t, "10", response)
}

func testSizeWithDirectories(t *testing.T, client *goftp.Client, raw goftp.RawConn) {
	t.Helper()
	returnCode, response, err := raw.SendCommand("TYPE I")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	_, err = client.Mkdir("a-dir")
	require.NoError(t, err)

	returnCode, response, err = raw.SendCommand("SIZE a-dir")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasSuffix(response, "is a directory"))

	// Generate a cryptographically secure random number for file size
	nBig, err := rand.Int(rand.Reader, big.NewInt(10))
	require.NoError(t, err)
	fileSize := 11 + int(nBig.Int64())
	ftpUpload(t, client, createTemporaryFile(t, fileSize), "a-dir/file.bin")

	returnCode, response, err = raw.SendCommand("SIZE a-dir/file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode, response)
	require.Equal(t, strconv.Itoa(fileSize), response)

	returnCode, response, err = raw.SendCommand("SIZE b-dir/file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasSuffix(response, "no such file or directory"))
}

func testSizeInASCIIMode(t *testing.T, raw goftp.RawConn) {
	t.Helper()
	returnCode, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	returnCode, response, err = raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.Equal(t, "SIZE not allowed in ASCII mode", response)

	returnCode, response, err = raw.SendCommand("SIZE a-dir")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.Equal(t, "SIZE not allowed in ASCII mode", response)

	returnCode, response, err = raw.SendCommand("SIZE a-dir/file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.Equal(t, "SIZE not allowed in ASCII mode", response)
}

func TestCOMBErrors(t *testing.T) {
	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	server.settings.EnableCOMB = true

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	returnCode, message, err := raw.SendCommand("COMB")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, message)

	returnCode, message, err = raw.SendCommand("COMB file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, returnCode, message)

	returnCode, message, err = raw.SendCommand("COMB file.bin missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, message)

	returnCode, message, err = raw.SendCommand("COMB /missing/file.bin file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, message)

	returnCode, message, err = raw.SendCommand("COMB file.bin \"\"")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, message)
}

type quotedParams struct {
	params    string
	parsed    []string
	wantError bool
}

func TestUnquoteCOMBParams(t *testing.T) {
	testQuotedParams := []quotedParams{
		{
			params:    "final5.log 64.log 65.log",
			parsed:    []string{"final5.log", "64.log", "65.log"},
			wantError: false,
		},
		{
			params:    "\"final3.log\" \"60à.log\"",
			parsed:    []string{"final3.log", "60à.log"},
			wantError: false,
		},
		{
			params:    "\"final5.log\" \"64.log\" \"65.log\"",
			parsed:    []string{"final5.log", "64.log", "65.log"},
			wantError: false,
		},
		{
			params:    "final7.log \"6 6.log\" 67.log",
			parsed:    []string{"final7.log", "6 6.log", "67.log"},
			wantError: false,
		},
		{
			params:    "",
			parsed:    nil,
			wantError: true,
		},
	}

	for _, params := range testQuotedParams {
		parsed, err := unquoteSpaceSeparatedParams(params.params)

		if params.wantError {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, params.parsed, parsed)
		}
	}
}

func getKnownHASHMappings() map[string]string {
	knownHASHMapping := make(map[string]string)
	knownHASHMapping["XCRC"] = "21b0f382"
	knownHASHMapping["MD5"] = "6905e38270e1797e68f69026bfbef131"
	knownHASHMapping["XMD5"] = "6905e38270e1797e68f69026bfbef131"
	knownHASHMapping["XSHA"] = "0f11c4103a2573b14edd4733984729f2380d99ed"
	knownHASHMapping["XSHA1"] = "0f11c4103a2573b14edd4733984729f2380d99ed"
	knownHASHMapping["XSHA256"] = "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b"
	knownHASHMapping["XSHA512"] = "4f95c20e4d030cbc43b1e139a0fe11c5e0e5e520cf3265bae852ae212b1c7cdb02c2fea5ba038cbf3202af8cdf313579fbe344d47919c288c16d6dd671e9db63" //nolint:lll

	return knownHASHMapping
}
