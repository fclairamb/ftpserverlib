// Package sample is a sample server driver
package drivers

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"sync/atomic"

	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"

	"github.com/jinzhu/configor"

	"github.com/r0123r/ftpserver/server"
)

// FilesDriver defines a very basic ftpserver driver
type FilesDriver struct {
	server.MainDriver
	SettingsFile string      // Settings file
	BaseDir      string      // Base directory from which to serve file
	tlsConfig    *tls.Config // TLS config (if applies)
	config       OurSettings // Our settings
	nbClients    int32       // Number of clients
}

// Account defines a user/pass password
type Account struct {
	User string // Username
	Pass string // Password
	Dir  string // Directory
}

// OurSettings defines our settings
type OurSettings struct {
	Server         server.Settings // Server settings (shouldn't need to be filled)
	Users          []Account       // Credentials
	MaxConnections int32           // Maximum number of clients that are allowed to connect at the same time
}

// GetSettings returns some general settings around the server setup
func (driver *FilesDriver) GetSettings() (*server.Settings, error) {
	//var config OurSettings
	var err error
	if err = configor.Load(&driver.config, driver.SettingsFile); err != nil {
		return nil, fmt.Errorf("problem loading \"%s\": %v", driver.SettingsFile, err)
	}

	if len(driver.config.Users) == 0 {
		return nil, errors.New("you must have at least one user defined")
	}

	return &driver.config.Server, nil
}

// GetTLSConfig returns a TLS Certificate to use
func (driver *FilesDriver) GetTLSConfig() (*tls.Config, error) {

	if driver.tlsConfig == nil {
		log.Println("msg", "Loading certificate")
		if cert, err := driver.getCertificate(); err == nil {
			driver.tlsConfig = &tls.Config{
				NextProtos:   []string{"ftp"},
				Certificates: []tls.Certificate{*cert},
			}
		} else {
			return nil, err
		}
	}
	return driver.tlsConfig, nil
}

// Live generation of a self-signed certificate
// This implementation of the driver doesn't load a certificate from a file on purpose. But it any proper implementation
// should most probably load the certificate from a file using tls.LoadX509KeyPair("cert_pub.pem", "cert_priv.pem").
func (driver *FilesDriver) getCertificate() (*tls.Certificate, error) {
	log.Println("msg", "Creating certificate")
	priv, err := rsa.GenerateKey(rand.Reader, 2048)

	if err != nil {
		log.Println("msg", "Could not generate key", "err", err)
		return nil, err
	}

	now := time.Now().UTC()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1337),
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"FTPServer"},
		},
		DNSNames:              []string{"localhost"},
		SignatureAlgorithm:    x509.SHA256WithRSA,
		PublicKeyAlgorithm:    x509.RSA,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour * 24 * 7),
		SubjectKeyId:          []byte{1, 2, 3, 4, 5},
		BasicConstraintsValid: true,
		IsCA:        false,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)

	if err != nil {
		log.Println("msg", "Could not create cert", "err", err)
		return nil, err
	}

	var certPem, keyPem bytes.Buffer
	if err := pem.Encode(&certPem, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, err
	}
	if err := pem.Encode(&keyPem, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		return nil, err
	}
	c, err := tls.X509KeyPair(certPem.Bytes(), keyPem.Bytes())
	return &c, err
}

// WelcomeUser is called to send the very first welcome message
func (driver *FilesDriver) WelcomeUser(cc server.ClientContext) (string, error) {
	nbClients := atomic.AddInt32(&driver.nbClients, 1)
	if nbClients > driver.config.MaxConnections {
		return "Cannot accept any additional client", fmt.Errorf("too many clients: %d > % d", driver.nbClients, driver.config.MaxConnections)
	}

	cc.SetDebug(true)
	// This will remain the official name for now
	return fmt.Sprintf(
			"Welcome on ftpserver, dir:%s, ID:%d, addr:%s, clients:%d",
			driver.BaseDir,
			cc.ID(),
			cc.RemoteAddr(),
			nbClients),
		nil
}

// AuthUser authenticates the user and selects an handling driver
func (driver *FilesDriver) AuthUser(cc server.ClientContext, user, pass string) (server.ClientHandlingDriver, error) {

	for _, act := range driver.config.Users {
		if act.User == user && act.Pass == pass {
			// If we are authenticated, we can return a client driver containing *our* basedir
			baseDir := driver.BaseDir + string(os.PathSeparator) + act.Dir
			os.MkdirAll(baseDir, 0777)
			return &ClientDriver{BaseDir: baseDir}, nil
		}
	}

	return nil, fmt.Errorf("could not authenticate you")
}

// UserLeft is called when the user disconnects, even if he never authenticated
func (driver *FilesDriver) UserLeft(cc server.ClientContext) {
	atomic.AddInt32(&driver.nbClients, -1)
}

// NewSampleDriver creates a sample driver
func NewSampleDriver(dir string, settingsFile string) (*FilesDriver, error) {
	if dir == "" {
		var err error
		dir, err = ioutil.TempDir("", "ftpserver")
		if err != nil {
			return nil, fmt.Errorf("could not find a temporary dir, err: %v", err)
		}
	}

	drv := &FilesDriver{
		SettingsFile: settingsFile,
		BaseDir:      dir,
	}

	return drv, nil
}
