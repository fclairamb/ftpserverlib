# Golang FTP Server library

![Build](https://github.com/fclairamb/ftpserverlib/workflows/Build/badge.svg)
[![codecov](https://codecov.io/gh/fclairamb/ftpserverlib/branch/master/graph/badge.svg?token=IVeoGgl1rj)](https://codecov.io/gh/fclairamb/ftpserverlib)
[![Go Report Card](https://goreportcard.com/badge/fclairamb/ftpserverlib)](https://goreportcard.com/report/fclairamb/ftpserverlib)
[![GoDoc](https://godoc.org/github.com/fclairamb/ftpserverlib?status.svg)](https://godoc.org/github.com/fclairamb/ftpserverlib)

This library allows to easily build a simple and fully-featured FTP server using [afero](https://github.com/spf13/afero) as the backend filesystem.

If you're interested in a fully featured FTP server, you should use [sftpgo](https://github.com/drakkan/sftpgo) (fully featured SFTP/FTP server) or [ftpserver](https://github.com/fclairamb/ftpserver) (basic FTP server).

## Current status of the project

### Features

 * Uploading and downloading files
 * Directory listing (LIST + MLST)
 * File and directory deletion and renaming
 * TLS support (AUTH + PROT)
 * File download/upload resume support (REST)
 * Passive socket connections (PASV and EPSV commands)
 * Active socket connections (PORT and EPRT commands)
 * IPv6 support (EPSV + EPRT)
 * Small memory footprint
 * Clean code: No sync, no sleep, no panic
 * Uses only the standard library except for:
   * [afero](https://github.com/spf13/afero) for generic file systems handling
   * [go-kit log](https://github.com/go-kit/kit/tree/master/log) (optional) for logging
 * Supported extensions:
   * [AUTH](https://tools.ietf.org/html/rfc2228#page-6) - Control session protection
   * [AUTH TLS](https://tools.ietf.org/html/rfc4217#section-4.1) - TLS session
   * [PROT](https://tools.ietf.org/html/rfc2228#page-8) - Transfer protection
   * [EPRT/EPSV](https://tools.ietf.org/html/rfc2428) - IPv6 support
   * [MDTM](https://tools.ietf.org/html/rfc3659#page-8) - File Modification Time
   * [SIZE](https://tools.ietf.org/html/rfc3659#page-11) - Size of a file
   * [REST](https://tools.ietf.org/html/rfc3659#page-13) - Restart of interrupted transfer
   * [MLST](https://tools.ietf.org/html/rfc3659#page-23) - Simple file listing for machine processing
   * [MLSD](https://tools.ietf.org/html/rfc3659#page-23) - Directory listing for machine processing
   * [HASH](https://tools.ietf.org/html/draft-bryan-ftpext-hash-02) - Hashing of files
   * [AVLB](https://tools.ietf.org/html/draft-peterson-streamlined-ftp-command-extensions-10#section-4) - Available space
   * [COMB](https://help.globalscape.com/help/archive/eft6-4/mergedprojects/eft/allowingmultiparttransferscomb_command.htm) - Combine files

## Quick test
The easiest way to test this library is to use [ftpserver](https://github.com/fclairamb/ftpserver).

## The driver
The simplest way to get a good understanding of how the driver shall be implemented, you can have a look at the [tests driver](https://github.com/fclairamb/ftpserverlib/blob/master/driver_test.go). 

### The base API

The API is directly based on [afero](https://github.com/spf13/afero).

```go
// MainDriver handles the authentication and ClientHandlingDriver selection
type MainDriver interface {
	// GetSettings returns some general settings around the server setup
	GetSettings() (*Settings, error)

	// ClientConnected is called to send the very first welcome message
	ClientConnected(cc ClientContext) (string, error)

	// ClientDisconnected is called when the user disconnects, even if he never authenticated
	ClientDisconnected(cc ClientContext)

	// AuthUser authenticates the user and selects an handling driver
	AuthUser(cc ClientContext, user, pass string) (ClientDriver, error)

	// GetTLSConfig returns a TLS Certificate to use
	// The certificate could frequently change if we use something like "let's encrypt"
	GetTLSConfig() (*tls.Config, error)
}


// ClientDriver is the base FS implementation that allows to manipulate files
type ClientDriver interface {
	afero.Fs
}

// ClientContext is implemented on the server side to provide some access to few data around the client
type ClientContext interface {
	// Path provides the path of the current connection
	Path() string

	// SetDebug activates the debugging of this connection commands
	SetDebug(debug bool)

	// Debug returns the current debugging status of this connection commands
	Debug() bool

	// Client's ID on the server
	ID() uint32

	// Client's address
	RemoteAddr() net.Addr

	// Servers's address
	LocalAddr() net.Addr

	// Client's version can be empty
	GetClientVersion() string

	// Close closes the connection and disconnects the client.
	Close() error

	// HasTLSForControl returns true if the control connection is over TLS
	HasTLSForControl() bool

	// HasTLSForTransfers returns true if the transfer connection is over TLS
	HasTLSForTransfers() bool

	// GetLastCommand returns the last received command
	GetLastCommand() string
}

// Settings define all the server settings
type Settings struct {
	Listener                 net.Listener     // (Optional) To provide an already initialized listener
	ListenAddr               string           // Listening address
	PublicHost               string           // Public IP to expose (only an IP address is accepted at this stage)
	PublicIPResolver         PublicIPResolver // (Optional) To fetch a public IP lookup
	PassiveTransferPortRange *PortRange       // (Optional) Port Range for data connections. Random if not specified
	ActiveTransferPortNon20  bool             // Do not impose the port 20 for active data transfer (#88, RFC 1579)
	IdleTimeout              int              // Maximum inactivity time before disconnecting (#58)
	ConnectionTimeout        int              // Maximum time to establish passive or active transfer connections
	DisableMLSD              bool             // Disable MLSD support
	DisableMLST              bool             // Disable MLST support
	DisableMFMT              bool             // Disable MFMT support (modify file mtime)
	Banner                   string           // Banner to use in server status response
	TLSRequired              TLSRequirement   // defines the TLS mode
	DisableLISTArgs          bool             // Disable ls like options (-a,-la etc.) for directory listing
	DisableSite              bool             // Disable SITE command
	DisableActiveMode        bool             // Disable Active FTP
	EnableHASH               bool             // Enable support for calculating hash value of files
	DisableSTAT              bool             // Disable Server STATUS, STAT on files and directories will still work
	DisableSYST              bool             // Disable SYST
	EnableCOMB               bool             // Enable COMB support
	DefaultTransferType      TransferType     // Transfer type to use if the client don't send the TYPE command
}
```

### Extensions
There are a few extensions to the base afero APIs so that you can perform some operations that aren't offered by afero.

#### Pre-allocate some space
```go
// ClientDriverExtensionAllocate is an extension to support the "ALLO" - file allocation - command
type ClientDriverExtensionAllocate interface {

	// AllocateSpace reserves the space necessary to upload files
	AllocateSpace(size int) error
}
```

#### Get available space
```go
// ClientDriverExtensionAvailableSpace is an extension to implement to support
// the AVBL ftp command
type ClientDriverExtensionAvailableSpace interface {
	GetAvailableSpace(dirName string) (int64, error)
}
```

#### Create symbolic link
```go
// ClientDriverExtensionSymlink is an extension to support the "SITE SYMLINK" - symbolic link creation - command
type ClientDriverExtensionSymlink interface {

	// Symlink creates a symlink
	Symlink(oldname, newname string) error

	// SymlinkIfPossible allows to get the source of a symlink (but we don't need for now)
	// ReadlinkIfPossible(name string) (string, error)
}
```

#### Compute file hash
```go
// ClientDriverExtensionHasher is an extension to implement if you want to handle file digests
// yourself. You have to set EnableHASH to true for this extension to be called
type ClientDriverExtensionHasher interface {
	ComputeHash(name string, algo HASHAlgo, startOffset, endOffset int64) (string, error)
}
```

## History of the project

I wanted to make a system which would accept files through FTP and redirect them to something else. Go seemed like the obvious choice and it seemed there was a lot of libraries available but it turns out none of them were in a useable state.

* [micahhausler/go-ftp](https://github.com/micahhausler/go-ftp) is a  minimalistic implementation 
* [shenfeng/ftpd.go](https://github.com/shenfeng/ftpd.go) is very basic and 4 years old.
* [yob/graval](https://github.com/yob/graval) is 3 years old and “experimental”.
* [goftp/server](https://github.com/goftp/server) seemed OK but I couldn't use it on both Filezilla and the MacOs ftp client.
* [andrewarrow/paradise_ftp](https://github.com/andrewarrow/paradise_ftp) - Was the only one of the list I could test right away. This is the project I forked from.
