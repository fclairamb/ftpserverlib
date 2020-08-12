package ftpserver

import (
	"regexp"
	"testing"

	"github.com/secsy/goftp"
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
		User:     "test",
		Password: "test",
	}

	var err error

	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, s.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	var raw goftp.RawConn

	if raw, err = c.OpenRawConn(); err != nil {
		t.Fatal("Couldn't open raw connection")
	}

	// Asking for too much (2MB)
	if rc, _, err := raw.SendCommand("ALLO 2000000"); err != nil || rc != 550 {
		t.Fatal("Should have been refused", err, rc)
	}

	// Asking for the right amount of space (500KB)
	if rc, _, err := raw.SendCommand("ALLO 500000"); err != nil || rc != 200 {
		t.Fatal("Should have been accepted", err, rc)
	}
}

func TestCHOWN(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     "test",
		Password: "test",
	}

	var err error

	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, s.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	var raw goftp.RawConn

	if raw, err = c.OpenRawConn(); err != nil {
		t.Fatal("Couldn't open raw connection")
	}

	// Asking for a user change that isn't authorized
	if rc, _, err := raw.SendCommand("SITE CHOWN user:group file"); err != nil || rc != 550 {
		t.Fatal("Should have been refused", err, rc)
	}

	// Asking for a user change that isn't authorized
	if rc, _, err := raw.SendCommand("SITE CHOWN user file"); err != nil || rc != 550 {
		t.Fatal("Should have been refused", err, rc)
	}

	// Asking for the right chown user
	if rc, _, err := raw.SendCommand("SITE CHOWN test:test file"); err != nil || rc != 200 {
		t.Fatal("Should have been accepted", err, rc)
	}

	// Asking for the right chown user
	if rc, _, err := raw.SendCommand("SITE CHOWN test file"); err != nil || rc != 200 {
		t.Fatal("Should have been accepted", err, rc)
	}

	// Asking for a chown on a file that doesn't exist
	if rc, _, err := raw.SendCommand("SITE CHOWN test file2"); rc != 550 {
		t.Fatal("Should NOT have been accepted", err, rc)
	}
}

func TestSYMLINK(t *testing.T) {
	s := NewTestServer(t, true)

	conf := goftp.Config{
		User:     "test",
		Password: "test",
	}

	var err error

	var c *goftp.Client

	if c, err = goftp.DialConfig(conf, s.Addr()); err != nil {
		t.Fatal("Couldn't connect", err)
	}

	defer func() { panicOnError(c.Close()) }()

	// Creating a tiny file
	ftpUpload(t, c, createTemporaryFile(t, 10), "file")

	var raw goftp.RawConn

	if raw, err = c.OpenRawConn(); err != nil {
		t.Fatal("Couldn't open raw connection")
	}

	// Creating a bad clunky is authorized
	if rc, _, err := raw.SendCommand("SITE SYMLINK file3 file4"); err != nil || rc != 200 {
		t.Fatal("Should have been accepted", err, rc)
	}

	// Overwriting a file is not authorized
	if rc, _, err := raw.SendCommand("SITE SYMLINK file5 file"); rc != 550 {
		t.Fatal("Should have been refused", err, rc)
	}

	// Good symlink
	if rc, _, err := raw.SendCommand("SITE SYMLINK file test"); err != nil || rc != 200 {
		t.Fatal("Should have been accepted", err, rc)
	}

}
