package ftpserver

import (
	"fmt"
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

	var err error

	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	// Asking for too much (2MB)
	rc, _, err := raw.SendCommand("ALLO 2000000")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc, "Should have been refused")

	// Asking for the right amount of space (500KB)
	rc, _, err = raw.SendCommand("ALLO 500000")
	require.NoError(t, err)
	require.Equal(t, StatusOK, rc, "Should have been accepted")
}

func TestCHOWN(t *testing.T) {
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

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

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
}

func TestMFMT(t *testing.T) {
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

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

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

	var err error

	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	// Creating a bad clunky is authorized
	rc, _, err := raw.SendCommand("SITE SYMLINK file3 file4")
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

	var err error

	var c *goftp.Client

	c, err = goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	// Create a directory with a subdir
	_, err = c.Mkdir("dir")
	require.NoError(t, err)

	_, err = c.Mkdir("/dir/sub")
	require.NoError(t, err)

	var raw goftp.RawConn

	raw, err = c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

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
