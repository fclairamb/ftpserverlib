package ftpserver

import (
	"crypto/tls"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/dutchcoders/goftp.v1"
)

const DirKnown = "known"

// TestDirAccess relies on LIST of files listing
func TestDirListing(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}})

	var connErr error

	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}

	defer func() { panicOnError(ftp.Quit()) }()

	if _, err := ftp.List("/"); err == nil {
		t.Fatal("We could list files before login")
	}

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if err := ftp.Mkd("/" + DirKnown); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	if lines, err := ftp.List("/"); err != nil {
		t.Fatal("Couldn't list files:", err)
	} else {
		found := false
		for _, line := range lines {
			line = line[0 : len(line)-2]
			if len(line) < 47 {
				break
			}
			fileName := line[47:]
			if fileName == DirKnown {
				found = true
			}
		}
		if !found {
			t.Fatal("Couldn't find the dir")
		}
	}
}

func TestDirListingPathArg(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}})

	var connErr error

	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}

	defer func() { panicOnError(ftp.Quit()) }()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	for _, dir := range []string{"/" + DirKnown, "/" + DirKnown + "/1"} {
		if err := ftp.Mkd(dir); err != nil {
			t.Fatal("Couldn't create dir:", err)
		}
	}

	if lines, err := ftp.List(DirKnown); err != nil {
		t.Fatal("Couldn't list files:", err)
	} else {
		found := false
		for _, line := range lines {
			line = line[0 : len(line)-2]
			if len(line) < 47 {
				break
			}
			fileName := line[47:]
			if fileName == "1" {
				found = true
			}
		}
		if !found {
			t.Fatal("Couldn't find the dir")
		}
	}

	if lines, err := ftp.List(""); err != nil {
		t.Fatal("Couldn't list files:", err)
	} else {
		found := false
		for _, line := range lines {
			line = line[0 : len(line)-2]
			if len(line) < 47 {
				break
			}
			fileName := line[47:]
			if fileName == DirKnown {
				found = true
			}
		}
		if !found {
			t.Fatal("Couldn't find the dir")
		}
	}
}

// TestDirAccess relies on LIST of files listing
func TestDirHandling(t *testing.T) {
	s := NewTestServer(t, true)

	var connErr error

	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}

	defer func() { panicOnError(ftp.Quit()) }()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if path, err := ftp.Pwd(); err != nil {
		t.Fatal("Couldn't test PWD", err)
	} else if path != "/" {
		t.Fatal("Bad path:", path)
	}

	if err := ftp.Cwd("/unknown"); err == nil {
		t.Fatal("We should have had an error")
	}

	if err := ftp.Mkd("/" + DirKnown); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	if entry, err := ftp.List("/"); err != nil {
		t.Fatal("Couldn't list files")
	} else {
		found := false
		for _, entry := range entry {
			pathentry := validMLSxEntryPattern.FindStringSubmatch(entry)
			if len(pathentry) != 2 {
				t.Errorf("MLSx file listing contains invalid entry: \"%s\"", entry)
			} else if pathentry[1] == DirKnown {
				found = true
			}
		}
		if !found {
			t.Error("Newly created dir was not found during listing of files")
		}
	}

	if err := ftp.Cwd("/" + DirKnown); err != nil {
		t.Fatal("Couldn't access the known dir:", err)
	}

	if err := ftp.Rmd("/" + DirKnown); err != nil {
		t.Fatal("Couldn't ftpDelete the known dir:", err)
	}

	if err := ftp.Rmd("/" + DirKnown); err == nil {
		t.Fatal("We shouldn't have been able to ftpDelete known again")
	}
}

// TestDirListingWithSpace uses the MLSD for files listing
func TestDirListingWithSpace(t *testing.T) {
	s := NewTestServer(t, true)

	var connErr error

	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}

	defer func() { panicOnError(ftp.Quit()) }()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if err := ftp.Mkd("/ with spaces "); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	if lines, err := ftp.List("/"); err != nil {
		t.Fatal("Couldn't list files:", err)
	} else {
		found := false
		for _, line := range lines {
			line = line[0 : len(line)-2]
			if len(line) < 47 {
				break
			}
			spl := strings.SplitN(line, "; ", 2)
			fileName := spl[1]
			expectedfileName := " with spaces "
			if runtime.GOOS == "windows" {
				expectedfileName = " with spaces"
			}
			if fileName == expectedfileName {
				found = true
			}
		}
		if !found {
			t.Fatal("Couldn't find the dir")
		}
	}

	if err := ftp.Cwd("/ with spaces "); err != nil {
		t.Fatal("Couldn't access the known dir:", err)
	}
}

func TestCleanPath(t *testing.T) {
	s := NewTestServer(t, true)

	var connErr error

	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}

	defer func() { panicOnError(ftp.Quit()) }()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	// various path purity tests

	for _, dir := range []string{
		"..",
		"../..",
		"/../..",
		"////",
		"/./",
		"/././.",
	} {
		if err := ftp.Cwd(dir); err != nil {
			t.Fatal("Couldn't Cwd to a valid path:", err)
		}

		if path, err := ftp.Pwd(); err != nil {
			t.Fatal("PWD failed:", err)
		} else if path != "/" {
			t.Fatal("Faulty path:", path)
		}
	}
}

func TestTLSTransfer(t *testing.T) {
	s := NewTestServerWithDriver(t, &TestServerDriver{
		Debug: true,
		TLS:   true,
	})
	s.settings.TLSRequired = 1

	ftp, err := goftp.Connect(s.Addr())
	if err != nil {
		t.Fatal("Couldn't connect:", err)
	}

	defer func() { reportError(ftp.Quit()) }()

	config := &tls.Config{
		// nolint:gosec
		InsecureSkipVerify: true,
	}
	if err = ftp.AuthTLS(config); err != nil {
		t.Fatal("Couldn't upgrade connection to TLS:", err)
	}

	if err = ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if _, err := ftp.List("/"); err != nil {
		t.Fatal("Couldn't list files")
	}

	code, _ := ftp.RawCmd("PROT C")
	if code != StatusOK {
		t.Fatal("unable to send PROT C")
	}

	if _, err := ftp.List("/"); err == nil {
		t.Fatal("List files should fail, TLS is required")
	} else if !strings.Contains(err.Error(), "TLS is required") {
		t.Fatal("unexpected error:", err)
	}
}
