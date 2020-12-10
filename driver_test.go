package ftpserver

import (
	"crypto/tls"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"testing"

	gklog "github.com/go-kit/kit/log"
	"github.com/spf13/afero"

	"github.com/fclairamb/ftpserverlib/log/gokit"
)

const (
	authUser    = "test"
	authPass    = "test"
	authUserID  = 1000
	authGroupID = 500
)

// NewTestServer provides a test server with or without debugging
func NewTestServer(t *testing.T, debug bool) *FtpServer {
	return NewTestServerWithDriver(t, &TestServerDriver{Debug: debug})
}

// NewTestServerWithDriver provides a server instantiated with some settings
func NewTestServerWithDriver(t *testing.T, driver *TestServerDriver) *FtpServer {
	t.Parallel()

	if driver.Settings == nil {
		driver.Settings = &Settings{}
	}

	if driver.Settings.ListenAddr == "" {
		driver.Settings.ListenAddr = "127.0.0.1:0"
	}

	{
		dir, _ := ioutil.TempDir("", "example")
		if err := os.MkdirAll(dir, 0750); err != nil {
			panic(err)
		}
		driver.fs = afero.NewBasePathFs(afero.NewOsFs(), dir)
	}

	s := NewFtpServer(driver)

	// If we are in debug mode, we should log things
	if driver.Debug {
		s.Logger = gokit.NewGKLogger(gklog.NewLogfmtLogger(gklog.NewSyncWriter(os.Stdout))).With(
			"ts", gokit.GKDefaultTimestampUTC,
			"caller", gokit.GKDefaultCaller,
		)
	}

	t.Cleanup(func() {
		mustStopServer(s)
	})

	if err := s.Listen(); err != nil {
		return nil
	}

	go func() {
		if err := s.Serve(); err != nil && err != io.EOF {
			s.Logger.Error("problem serving", "err", err)
		}
	}()

	return s
}

// TestServerDriver defines a minimal serverftp server driver
type TestServerDriver struct {
	Debug bool // To display connection logs information
	TLS   bool

	Settings     *Settings // Settings
	FileOverride afero.File
	fs           afero.Fs
}

// TestClientDriver defines a minimal serverftp client driver
type TestClientDriver struct {
	FileOverride afero.File
	afero.Fs
}

// NewTestClientDriver creates a client driver
func NewTestClientDriver(server *TestServerDriver) *TestClientDriver {
	return &TestClientDriver{
		Fs: server.fs,
	}
}

func mustStopServer(server *FtpServer) {
	err := server.Stop()
	if err != nil {
		panic(err)
	}
}

// ClientConnected is the very first message people will see
func (driver *TestServerDriver) ClientConnected(cc ClientContext) (string, error) {
	cc.SetDebug(driver.Debug)
	// This will remain the official name for now
	return "TEST Server", nil
}

var errBadUserNameOrPassword = errors.New("bad username or password")

// AuthUser with authenticate users
func (driver *TestServerDriver) AuthUser(_ ClientContext, user, pass string) (ClientDriver, error) {
	if user == authUser && pass == authPass {
		clientdriver := NewTestClientDriver(driver)

		if driver.FileOverride != nil {
			clientdriver.FileOverride = driver.FileOverride
		}

		return clientdriver, nil
	}

	return nil, errBadUserNameOrPassword
}

// ClientDisconnected is called when the user disconnects
func (driver *TestServerDriver) ClientDisconnected(ClientContext) {

}

// GetSettings fetches the basic server settings
func (driver *TestServerDriver) GetSettings() (*Settings, error) {
	return driver.Settings, nil
}

// GetTLSConfig fetches the TLS config
func (driver *TestServerDriver) GetTLSConfig() (*tls.Config, error) {
	if driver.TLS {
		keypair, err := tls.X509KeyPair(localhostCert, localhostKey)
		if err != nil {
			return nil, err
		}

		return &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{keypair},
		}, nil
	}

	return nil, nil
}

// OpenFile opens a file in 3 possible modes: read, write, appending write (use appropriate flags)
func (driver *TestClientDriver) OpenFile(path string, flag int, perm os.FileMode) (afero.File, error) {
	if driver.FileOverride != nil {
		return driver.FileOverride, nil
	}

	return driver.Fs.OpenFile(path, flag, perm)
}

var errTooMuchSpaceRequested = errors.New("you're requesting too much space")

func (driver *TestClientDriver) AllocateSpace(size int) error {
	if size < 1*1024*1024 {
		return nil
	}

	return errTooMuchSpaceRequested
}

var errInvalidChownUser = errors.New("invalid chown on user")
var errInvalidChownGroup = errors.New("invalid chown on group")

func (driver *TestClientDriver) Chown(name string, uid int, gid int) error {
	if uid != 0 && uid != authUserID {
		return errInvalidChownUser
	}

	if gid != 0 && gid != authGroupID {
		return errInvalidChownGroup
	}

	_, err := driver.Fs.Stat(name)

	return err
}

var errSymlinkNotImplemented = errors.New("symlink not implemented")

func (driver *TestClientDriver) Symlink(oldname, newname string) error {
	if linker, ok := driver.Fs.(afero.Linker); ok {
		return linker.SymlinkIfPossible(oldname, newname)
	}

	return errSymlinkNotImplemented
}

// (copied from net/http/httptest)
// localhostCert is a PEM-encoded TLS cert with SAN IPs
// "127.0.0.1" and "[::1]", expiring at the last second of 2049 (the end
// of ASN.1 time).
// generated from src/crypto/tls:
// go run "$(go env GOROOT)/src/crypto/tls/generate_cert.go" \
//   --rsa-bits 2048 \
//   --host 127.0.0.1,::1,example.com \
//   --ca --start-date "Jan 1 00:00:00 1970" \
//   --duration=1000000h
// The initial 512 bits key caused this error:
// "tls: failed to sign handshake: crypto/rsa: key size too small for PSS signature"
var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIIDGTCCAgGgAwIBAgIRAJ5VaFcqzaSMmEpeZc33uuowDQYJKoZIhvcNAQELBQAw
EjEQMA4GA1UEChMHQWNtZSBDbzAgFw03MDAxMDEwMDAwMDBaGA8yMDg0MDEyOTE2
MDAwMFowEjEQMA4GA1UEChMHQWNtZSBDbzCCASIwDQYJKoZIhvcNAQEBBQADggEP
ADCCAQoCggEBAMmv1cldip1/97VnNpPElc5Msa69Cx9l2LmtPubok3pcQy4lS/uF
1zlMDwFseRYuYMOy+lafmYsCO1OFQvt4dginlSZ9yUNq7qSv+dvOUpn6bWQdrLto
d+fDWS4KWiiFsyS78ITozMyRS3G9mJS8YSbGuV4O50UpOJQd6yN5pMQEnp/wHfRI
6y9OYOjWe2snw3rXq1wN7wkj4iVKgrqkJJUHe7Heq4uD7uGfABfOyACmzYFxexXN
f+++L/DesKyMH2At+nKmBtF3JixViIyVKpsCz6Lce1P90n39lYuQDHbV/N2P7ww8
fiwH7fA30yfDqxWKUXhxu7eHGxD1GCFBpgcCAwEAAaNoMGYwDgYDVR0PAQH/BAQD
AgKkMBMGA1UdJQQMMAoGCCsGAQUFBwMBMA8GA1UdEwEB/wQFMAMBAf8wLgYDVR0R
BCcwJYILZXhhbXBsZS5jb22HBH8AAAGHEAAAAAAAAAAAAAAAAAAAAAEwDQYJKoZI
hvcNAQELBQADggEBAHwPqImQQHsqao5MSmhd3S8XQiH6V95T2qOiF9pri7TfMF8h
JWhkaFKQ0yMi37reSZ5+cBiHO9z3UIAmpAWLdqbtVOnh2bchVMO8nSnKHkrOqV2E
IK0Fq5QVW2wyHlYaOMLLQ2sA4I3J/yHl6W4rigetEzY8OtQtPFbg/S1YMqFV8mRz
8PAxtrOWK+ARJP9tqgbylcL6/6cZc6lBQcs0BuwXjcI6fxi+YBXEqbpah9tGRivD
X/k0l93dx/zfNc1Yz06VrCpko6W2Kqa76F6tDIa+WpfZba7t7jNFZTPB4dymUS9L
ICoGMF9k6xscqAURRx8RoSiELemGE5kYUsyvqSE=
-----END CERTIFICATE-----`)

// localhostKey is the private key for localhostCert.
var localhostKey = []byte(`-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDJr9XJXYqdf/e1
ZzaTxJXOTLGuvQsfZdi5rT7m6JN6XEMuJUv7hdc5TA8BbHkWLmDDsvpWn5mLAjtT
hUL7eHYIp5UmfclDau6kr/nbzlKZ+m1kHay7aHfnw1kuCloohbMku/CE6MzMkUtx
vZiUvGEmxrleDudFKTiUHesjeaTEBJ6f8B30SOsvTmDo1ntrJ8N616tcDe8JI+Il
SoK6pCSVB3ux3quLg+7hnwAXzsgAps2BcXsVzX/vvi/w3rCsjB9gLfpypgbRdyYs
VYiMlSqbAs+i3HtT/dJ9/ZWLkAx21fzdj+8MPH4sB+3wN9Mnw6sVilF4cbu3hxsQ
9RghQaYHAgMBAAECggEADmJN+viC5Ey2G+fqiotgq7/ohC/TVT/sPwHOFKXNrtJZ
sDbUvnGDMgDsqQtVb3GLUSm4lOj5CGL2XDSK3Ghw8pkRGBesfPRpZLFwPm7ukTC9
EIDVSuBefNb/yzrNx0oRxrLoqnH3+Tb7jHcbJLBytVNC8SRa9iHEeTvRA0yvpZMW
WriTbAELv+Zcjal2fPYYtTE9HnRpJX7kHvnCRzlGza0MIs8Q4QgmBE20GRCEXaRi
4jPYjlBx/N4mdD1MTz9jAq+WCHQNJS6aic6l5jidemsSDjtLkSIy8mpTSbA83BTe
qkjAbxtSQ5FKYYH6zDhNbGKwmyqaF1g5gMPSFaDjUQKBgQDfsXamsiyf2Er1+WyI
WyFxwRJmDJ8y3IlH5z5d8Gw+b3JFt72Cy6MVYsO564UALQgUwJmxFIjnVh5F/ZA5
nwsfyyfUkpwQtiqZOTzeTnMd1wt4MPmGwaxfGwVhG5fUgYKnt1GTyF4zHz653RoL
AA0hhsiVmd4hb53PfVHEMVPEewKBgQDm0LzTkgz4zYgocRxiqa4n62VngRS2l7vs
oBgf6o7Dssp1aOucM5uryqzOZsAB/BwCVVeVTnC5nCL2os59YFWbBLlt15l7ykBo
HvUwfmf0R+81onMDqjYPj1+9CSKw4BbTD0WMBOUehvMpL6/k9CsAC2jXQ0oH735V
7dQHEZ1s5QKBgGNbGn1eBE4XLuxkFd3WxFsXS4nCL2/S3rLuNhhZcmqk65elzenr
cwtLq+3He3KhjcZR6bHqkghWiunBfy7ownMjtBRJ7kHJ98/IyY1gQOdPHcwLzLkb
CunPQatpKx37TEIcPYKra5O/XAgH+cpLAooSqMMx7aTiQ7DmU8wVsMRDAoGAQHcM
RgsElHjTDniI9QVvHrcgG0hyAI1gbzZHhqJ8PSwyX5huNbI0SEbS/NK1zdgb+orb
a1f9I9n36eqOwXWmcyVepM8SjwBt/Kao1GJ5pkBxDwnQFbX0Y2Qn2SQ0DDKKLWiW
hATZ+Sy3vUkUV13apKiLH5QrmQvKvTUvgsnorgECgYEAsuf9V7HXDVL3Za1imywP
B8waIgXRIjSWT4Fje7RTMT948qhguVhpoAgVzwzMqizzq6YIQbL7MHwXj7oZNUoQ
CARLpnYLaeWP2nxQyzwGx5pn9TJwg79Yknr8PbSjeym1BSbE5C9ruqar4PfiIzYx
di02m2YJAvRsG9VDpXogi+c=
-----END PRIVATE KEY-----`)
