// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/tls"
	"io"
	"net"
	"os"

	"github.com/spf13/afero"
)

// This file is the driver part of the server. It must be implemented by anyone wanting to use the server.

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

// ClientDriverExtensionAllocate is an extension to support the "ALLO" - file allocation - command
type ClientDriverExtensionAllocate interface {

	// AllocateSpace reserves the space necessary to upload files
	AllocateSpace(size int) error
}

/*
// ClientDriverExtensionChown is an extension to support the "CHOWN" - owner change - command
type ClientDriverExtensionChown interface {

	// Chown changes the owner of a file
	Chown(name string, user string, group string) error
}
*/

// ClientDriverExtensionSymlink is an extension to support the "SITE SYMLINK" - symbolic link creation - command
type ClientDriverExtensionSymlink interface {

	// Symlink creates a symlink
	Symlink(oldname, newname string) error

	// SymlinkIfPossible allows to get the source of a symlink (but we don't need for now)
	// ReadlinkIfPossible(name string) (string, error)
}

// ClientDriverExtensionFileList is a convenience extension to allow to return file listing
// without requiring to implement the methods Open/Readdir for your custom afero.File
type ClientDriverExtensionFileList interface {

	// ReadDir reads the directory named by name and return a list of directory entries.
	ReadDir(name string) ([]os.FileInfo, error)
}

// ClientDriverExtentionFileTransfer is a convenience extension to allow to transfer files
// without requiring to implement the methods Create/Open/OpenFile for your custom afero.File.
type ClientDriverExtentionFileTransfer interface {

	// GetHandle return an handle to upload or download a file based on flags:
	// os.O_RDONLY indicates a download
	// os.O_WRONLY indicates an upload and can be combined with os.O_APPEND (resume) or
	// os.O_CREATE (upload to new file/truncate)
	//
	// offset is the argument of a previous REST command, if any, or 0
	GetHandle(name string, flags int, offset int64) (FileTransfer, error)
}

// ClientDriverExtensionRemoveDir is an extension to implement if you need to distinguish
// between the FTP command DELE (remove a file) and RMD (remove a dir). If you don't
// implement this extension they will be both mapped to the Remove method defined in your
// afero.Fs implementation
type ClientDriverExtensionRemoveDir interface {
	RemoveDir(name string) error
}

// ClientDriverExtensionHasher is an extension to implement if you want to handle file digests
// yourself. You have to set EnableHASH to true for this extension to be called
type ClientDriverExtensionHasher interface {
	ComputeHash(name string, algo HASHAlgo, startOffset, endOffset int64) (string, error)
}

// ClientDriverExtensionAvailableSpace is an extension to implement to support
// the AVBL ftp command
type ClientDriverExtensionAvailableSpace interface {
	GetAvailableSpace(dirName string) (int64, error)
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

// FileTransfer defines the inferface for file transfers.
type FileTransfer interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
}

// FileTransferError is a FileTransfer extension used to notify errors.
type FileTransferError interface {
	TransferError(err error)
}

// PortRange is a range of ports
type PortRange struct {
	Start int // Range start
	End   int // Range end
}

// PublicIPResolver takes a ClientContext for a connection and returns the public IP
// to use in the response to the PASV command, or an error if a public IP cannot be determined.
type PublicIPResolver func(ClientContext) (string, error)

// TLSRequirement is the enumerable that represents the supported TLS mode
type TLSRequirement int

// TLS modes
const (
	ClearOrEncrypted TLSRequirement = iota
	MandatoryEncryption
	ImplicitEncryption
)

// Settings defines all the server settings
// nolint: maligned
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
