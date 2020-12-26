package ftpserver

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/secsy/goftp"
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

	// Asking for too much (2MB)
	rc, _, err := raw.SendCommand("ALLO 2000000")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	// Asking for the right amount of space (500KB)
	rc, _, err = raw.SendCommand("ALLO 500000")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")

	// Wrong size
	rc, _, err = raw.SendCommand("ALLO 500000a")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have been refused")
}

func TestCHMOD(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})
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

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("SITE CHMOD a file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	rc, _, err = raw.SendCommand("SITE CHMOD 600 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")
}

func TestCHOWN(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Asking for a chown user change that isn't authorized
	rc, _, err := raw.SendCommand("SITE CHOWN 1001:500 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	// Asking for a chown user change that isn't authorized
	rc, _, err = raw.SendCommand("SITE CHOWN 1001 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	// Asking for the right chown user
	rc, _, err = raw.SendCommand("SITE CHOWN 1000:500 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")

	// Asking for the right chown user
	rc, _, err = raw.SendCommand("SITE CHOWN 1000 file")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")

	// Asking for a chown on a file that doesn't exist
	rc, _, err = raw.SendCommand("SITE CHOWN test file2")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should NOT have been accepted")

	// Asking for a chown with a missing parameter
	rc, _, err = raw.SendCommand("SITE CHOWN 1000")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should NOT have been accepted")
}

func TestMFMT(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})
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

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Good
	rc, _, err := raw.SendCommand("MFMT 20201209211059 file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc, "Should have succeeded")

	// 3 params instead of 2
	rc, _, err = raw.SendCommand("MFMT 20201209211059 file somethingelse")
	require.NoError(t, err)
	require.NotEqual(t, StatusFileStatus, rc, "Should have failed")

	// 1 param instead of 2
	rc, _, err = raw.SendCommand("MFMT 202012092110 file")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have failed")

	// no parameters
	rc, _, err = raw.SendCommand("MFMT")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc, "Should have failed")

	// Good (to make sure we are still in sync)
	rc, _, err = raw.SendCommand("MFMT 20201209211059 file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc, "Should have succeeded")
}

func TestSYMLINK(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// Bad syntaxes
	rc, _, err := raw.SendCommand("SITE SYMLINK")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have been refused")

	rc, _, err = raw.SendCommand("SITE SYMLINK ")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have been refused")

	rc, _, err = raw.SendCommand("SITE SYMLINK file1")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have been refused")

	rc, _, err = raw.SendCommand("SITE SYMLINK file1 file2 file3")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, "Should have been refused")

	// Creating a bad symlink is authorized
	rc, _, err = raw.SendCommand("SITE SYMLINK file3 file4")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")

	// Overwriting a file is not authorized
	rc, _, err = raw.SendCommand("SITE SYMLINK file5 file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	// disable SITE
	s.settings.DisableSite = true

	rc, _, err = raw.SendCommand("SITE SYMLINK file test")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc, "Should have been refused")

	s.settings.DisableSite = false

	// Good symlink
	rc, _, err = raw.SendCommand("SITE SYMLINK file test")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")
}

func TestSTATFile(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	// Create a directory with a subdir
	_, err = c.Mkdir("dir")
	require.NoError(t, err)

	_, err = c.Mkdir("/dir/sub")
	require.NoError(t, err)

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("STAT file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)

	rc, _, err = raw.SendCommand("STAT dir")
	require.NoError(t, err)
	require.Equal(t, StatusDirectoryStatus, rc)

	// finally stat a missing file dir
	rc, _, err = raw.SendCommand("STAT missing")
	require.NoError(t, err)
	require.Equal(t, StatusFileActionNotTaken, rc)
}

func TestMDTM(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("MDTM file")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc)

	rc, _, err = raw.SendCommand("MDTM missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
}

func TestRename(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}
	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	err = c.Rename("file", "file1")
	require.NoError(t, err)

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, _, err := raw.SendCommand("RNTO file2")
	require.NoError(t, err)
	require.Equal(t, StatusBadCommandSequence, rc)
}

func TestHASHDisabled(t *testing.T) {
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

	rc, message, err := raw.SendCommand("XSHA256 file.txt")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, rc, message)
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

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

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
	s := NewTestServer(t, true)
	s.settings.EnableHASH = true
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	tempFile, err := ioutil.TempFile("", "ftpserver")
	require.NoError(t, err)
	_, err = tempFile.Write([]byte("sample data with know checksum/hash\n"))
	require.NoError(t, err)

	ftpUpload(t, c, tempFile, "file.txt")

	err = tempFile.Close()
	require.NoError(t, err)

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	hashMapping := getKnownHASHMappings()

	var rc int
	var message string

	for cmd, expected := range hashMapping {
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

func TestCOMB(t *testing.T) {
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

	rc, message, err := raw.SendCommand("COMB file.bin 1 2")
	require.NoError(t, err)
	require.Equal(t, StatusCommandNotImplemented, rc, message)

	s.settings.EnableCOMB = true

	var parts []*os.File

	partSize := 1024
	hasher := sha256.New()

	parts = append(parts, createTemporaryFile(t, partSize), createTemporaryFile(t, partSize),
		createTemporaryFile(t, partSize), createTemporaryFile(t, partSize))

	for idx, part := range parts {
		ftpUpload(t, c, part, fmt.Sprintf("%d", idx))
		_, err = part.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, err = io.Copy(hasher, part)
		require.NoError(t, err)
	}

	rc, message, err = raw.SendCommand("COMB file.bin 0 1 2 3")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc, message)
	require.Equal(t, "COMB succeeded!", message)

	info, err := c.Stat("file.bin")
	require.NoError(t, err)
	require.Equal(t, int64(partSize*4), info.Size())

	hashParts := hex.EncodeToString(hasher.Sum(nil))
	hashCombined := ftpDownloadAndHash(t, c, "file.bin")
	require.Equal(t, hashParts, hashCombined)

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
}

func TestCOMBAppend(t *testing.T) {
	s := NewTestServerWithDriver(
		t,
		&TestServerDriver{
			Debug: true,
			Settings: &Settings{
				EnableCOMB: true,
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

	partSize := 1024
	hasher := sha256.New()

	initialFile := createTemporaryFile(t, partSize)
	ftpUpload(t, c, initialFile, "file.bin")

	_, err = initialFile.Seek(0, io.SeekStart)
	require.NoError(t, err)
	_, err = io.Copy(hasher, initialFile)
	require.NoError(t, err)

	var parts []*os.File

	parts = append(parts, createTemporaryFile(t, partSize), createTemporaryFile(t, partSize))

	for idx, part := range parts {
		ftpUpload(t, c, part, fmt.Sprintf(" %d ", idx))
		_, err = part.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, err = io.Copy(hasher, part)
		require.NoError(t, err)
	}

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, message, err := raw.SendCommand("COMB file.bin \" 0 \" \" 1 \"")
	require.NoError(t, err)
	require.Equal(t, StatusFileOK, rc, message)
	require.Equal(t, "COMB succeeded!", message)

	info, err := c.Stat("file.bin")
	require.NoError(t, err)
	require.Equal(t, int64(partSize*3), info.Size())

	hashParts := hex.EncodeToString(hasher.Sum(nil))
	hashCombined := ftpDownloadAndHash(t, c, "file.bin")
	require.Equal(t, hashParts, hashCombined)

	contents, err := c.ReadDir("/")
	require.NoError(t, err)
	require.Len(t, contents, 1)
}

func TestREST(t *testing.T) {
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

	rc, response, err := raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	rc, response, err = raw.SendCommand("REST 10")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, response)

	rc, response, err = raw.SendCommand("TYPE I")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	rc, response, err = raw.SendCommand("REST a")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, response)
	require.True(t, strings.HasPrefix(response, "Couldn't parse size"))
}

func TestSIZE(t *testing.T) {
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

	rc, response, err := raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, response)
	require.True(t, strings.HasPrefix(response, "Couldn't access"))

	ftpUpload(t, c, createTemporaryFile(t, 10), "file.bin")

	rc, response, err = raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusFileStatus, rc, response)
	require.Equal(t, "10", response)

	rc, response, err = raw.SendCommand("TYPE A")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, response)

	rc, response, err = raw.SendCommand("SIZE file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, response)
	require.Equal(t, "SIZE not allowed in ASCII mode", response)
}

func TestCOMBErrors(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	s.settings.EnableCOMB = true

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	rc, message, err := raw.SendCommand("COMB")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, message)

	rc, message, err = raw.SendCommand("COMB file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorParameters, rc, message)

	rc, message, err = raw.SendCommand("COMB file.bin missing")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, message)

	rc, message, err = raw.SendCommand("COMB /missing/file.bin file.bin")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, message)

	rc, message, err = raw.SendCommand("COMB file.bin \"\"")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, message)
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

	for _, p := range testQuotedParams {
		parsed, err := unquoteSpaceSeparatedParams(p.params)

		if p.wantError {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, p.parsed, parsed)
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
