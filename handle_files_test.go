package ftpserver

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/secsy/goftp"
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

	returnCode, _, err = raw.SendCommand("STAT dir")
	require.NoError(t, err)
	require.Equal(t, StatusDirectoryStatus, returnCode)

	// finally stat a missing file dir
	returnCode, _, err = raw.SendCommand("STAT missing")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, returnCode)

	// the test driver will fail to open this dir
	dirName, err := client.Mkdir("fail-to-open")
	require.NoError(t, err)

	returnCode, _, err = raw.SendCommand(fmt.Sprintf("STAT %v", dirName))
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
		assert.True(t, strings.Contains(err.Error(), "553-Couldn't rename"), err.Error())
	}

	// renaming a missing file must fail
	err = client.Rename("missingfile", "file1")
	if assert.Error(t, err) {
		assert.True(t, strings.Contains(err.Error(), "550-Couldn't access"), err.Error())
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
	err = os.WriteFile(tempFile.Name(), []byte("sample data with know checksum/hash\n"), os.ModePerm)
	require.NoError(t, err)

	crc32Sum := "21b0f382"
	sha256Hash := "ceee704dd96e2b8c2ceca59c4c697bc01123fb9e66a1a3ac34dbdd2d6da9659b"

	ftpUpload(t, client, tempFile, "file.txt")

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// ask hash for a directory
	returnCode, _, err := raw.SendCommand(fmt.Sprintf("XSHA256 %v", dir))
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTakenNoFile, returnCode)

	// test the HASH command
	returnCode, message, err := raw.SendCommand("HASH file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode)
	require.True(t, strings.HasSuffix(message, fmt.Sprintf("SHA-256 0-36 %v file.txt", sha256Hash)))

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
		returnCode, message, err = raw.SendCommand(fmt.Sprintf("%v file.txt", cmd))
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

	var parts []*os.File

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

	var parts []*os.File

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

	returnCode, response, err := raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, returnCode, response)
	require.True(t, strings.HasPrefix(response, "Couldn't access"))

	ftpUpload(t, client, createTemporaryFile(t, 10), "file.bin")

	returnCode, response, err = raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, returnCode, response)
	require.Equal(t, "10", response)

	returnCode, response, err = raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, returnCode, response)

	returnCode, response, err = raw.SendCommand("SIZE file.bin")
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
