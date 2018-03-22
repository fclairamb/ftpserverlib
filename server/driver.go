package server

import (
	"crypto/tls"
	"io"
	"net"
	"os"
)

// This file is the driver part of the server. It must be implemented by anyone wanting to use the server.

// MainDriver handles the authentication and ClientHandlingDriver selection
type MainDriver interface {
	// GetSettings returns some general settings around the server setup
	GetSettings() (*Settings, error)

	// WelcomeUser is called to send the very first welcome message
	WelcomeUser(cc ClientContext) (string, error)

	// UserLeft is called when the user disconnects, even if he never authenticated
	UserLeft(cc ClientContext)

	// AuthUser authenticates the user and selects an handling driver
	AuthUser(cc ClientContext, user, pass string) (ClientHandlingDriver, error)

	// GetTLSConfig returns a TLS Certificate to use
	// The certificate could frequently change if we use something like "let's encrypt"
	GetTLSConfig() (*tls.Config, error)
}

// ClientHandlingDriver handles the file system access logic
type ClientHandlingDriver interface {
	// ChangeDirectory changes the current working directory
	ChangeDirectory(cc ClientContext, directory string) error

	// MakeDirectory creates a directory
	MakeDirectory(cc ClientContext, directory string) error

	// ListFiles lists the files of a directory
	ListFiles(cc ClientContext) ([]os.FileInfo, error)

	// OpenFile opens a file in 3 possible modes: read, write, appending write (use appropriate flags)
	OpenFile(cc ClientContext, path string, flag int) (FileStream, error)

	// DeleteFile deletes a file or a directory
	DeleteFile(cc ClientContext, path string) error

	// GetFileInfo gets some info around a file or a directory
	GetFileInfo(cc ClientContext, path string) (os.FileInfo, error)

	// RenameFile renames a file or a directory
	RenameFile(cc ClientContext, from, to string) error

	// CanAllocate gives the approval to allocate some data
	CanAllocate(cc ClientContext, size int) (bool, error)

	// ChmodFile changes the attributes of the file
	ChmodFile(cc ClientContext, path string, mode os.FileMode) error
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
}

// FileStream is a read or write closeable stream
type FileStream interface {
	io.Writer
	io.Reader
	io.Closer
	io.Seeker
}

// PortRange is a range of ports
type PortRange struct {
	Start int // Range start
	End   int // Range end
}

// PublicIPResolver takes a ClientContext for a connection and returns the public IP
// to use in the response to the PASV command, or an error if a public IP cannot be determined.
type PublicIPResolver func(ClientContext) (string, error)

// Type that takes a net.Listener and returns a net.Listener, potentially wrapping or modifying the behavior of
// the provided listener.
// This can be used for things like handling the proxy protocol header on connections.
type ListenerWrapper func(net.Listener) net.Listener

// Settings defines all the server settings
type Settings struct {
	ListenerWrapper           ListenerWrapper
	Listener                  net.Listener     // Allow providing an already initialized listener. Mutually exclusive with ListenAddr
	ListenAddr                string           // Listening address
	PublicHost                string           // Public IP to expose (only an IP address is accepted at this stage)
	PublicIPResolver          PublicIPResolver // Optional function that can perform a public ip lookup for the given CientContext.
	DataPortRange             *PortRange       // Port Range for data connections. Random one will be used if not specified
	DisableMLSD               bool             // Disable MLSD support
	DisableMLST               bool             // Disable MLST support
	NonStandardActiveDataPort bool             // Allow to use a non-standard active data port
	IdleTimeout               int              // Maximum inactivity time before disconnecting (#58)
}
