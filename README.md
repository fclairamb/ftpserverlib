# Golang FTP Server library

![Build](https://github.com/fclairamb/ftpserverlib/workflows/Build/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/fclairamb/ftpserverlib)](https://goreportcard.com/report/fclairamb/ftpserverlib)
[![GoDoc](https://godoc.org/github.com/fclairamb/ftpserverlib?status.svg)](https://godoc.org/github.com/fclairamb/ftpserverlib)

This library allows to easily build a simple and fully-featured FTP server using [afero](https://github.com/spf13/afero) as the backend filesystem.

If you're interested in a fully featured FTP server, you should use [ftpserver](https://github.com/fclairamb/ftpserver) 
or [sftpgo](https://github.com/drakkan/).

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

## Quick test
We are providing a server so that you can test how the library behaves.

```sh
# Get and install the server
go install github.com/fclairamb/ftpserver

# Create a storage dir
mkdir -p data

ftpserver -data data &

# Download some file
if [ ! -f file.bin ]; then
    wget -O file.bin.tmp https://github.com/fclairamb/ftpserver/releases/download/v0.5/ftpserver-linux-amd64 && mv file.bin.tmp file.bin
fi

# Connecting to the server and uploading the file
ftp ftp://test:test@localhost:2121
put file.bin
quit
ls -lh data/file.bin
```

## Quick test with docker
There's also a containerized version of the demo server (15MB, based on alpine).

```sh
# Creating a storage dir
mkdir -p data

# Starting the sample FTP server
docker run --rm -d -p 2121-2130:2121-2130 -v $(pwd)/data:/data fclairamb/ftpserver

# Download some file
if [ ! -f kitty.jpg ]; then
    curl -o kitty.jpg.tmp https://placekitten.com/2048/2048 && mv kitty.jpg.tmp kitty.jpg
fi

curl -v -T kitty.bin ftp://test:test@localhost:2121/
```

## The driver

### The API

The API is directly based on [afero](https://github.com/spf13/afero).
```go
// ServerDriver handles the authentication and ClientHandlingDriver selection
type ServerDriver interface {
	// Load some general settings around the server setup
	GetSettings() *Settings

	// WelcomeUser is called to send the very first welcome message
	WelcomeUser(cc ClientContext) (string, error)

	// UserLeft is called when the user disconnects, even if he never authenticated
	UserLeft(cc ClientContext)

	// AuthUser authenticates the user and selects an handling driver
	AuthUser(cc ClientContext, user, pass string) (afero.Fs, error)

	// GetCertificate returns a TLS Certificate to use
	// The certificate could frequently change if we use something like "let's encrypt"
	GetTLSConfig() (*tls.Config, error)
}

// ClientContext is implemented on the server side to provide some access to few data around the client
type ClientContext interface {
	// Get current path
	Path() string

	// SetDebug activates the debugging of this connection commands
	SetDebug(debug bool)

	// Debug returns the current debugging status of this connection commands
	Debug() bool
	
	// Client's ID on the server
	ID() uint32

	// Client's address
	RemoteAddr() net.Addr
}

// Settings define all the server settings
type Settings struct {
	ListenHost     string     // Host to receive connections on
	ListenPort     int        // Port to listen on
	PublicHost     string     // Public IP to expose (only an IP address is accepted at this stage)
	DataPortRange  *PortRange // Port Range for data connections. Random one will be used if not specified
}
```

### Sample implementation

Have a look at the [sample driver](https://github.com/fclairamb/ftpserver/tree/master/sample). It shows how you can plug your FTP server to something else, in this case your file system.

## Sample run
```
$ ftp ftp://a:a@localhost:2121
Trying ::1...
Connected to localhost.
220 Welcome on https://github.com/fclairamb/ftpserver
331 OK
230 Password ok, continue
Remote system type is UNIX.
Using binary mode to transfer files.
200 Type set to binary
ftp> put iMX7D_RM_Rev_B.pdf 
local: iMX7D_RM_Rev_B.pdf remote: iMX7D_RM_Rev_B.pdf
229 Entering Extended Passive Mode (|||62362|)
150 Using transfer connection
100% |******************************************************************************************************************************************************************| 44333 KiB  635.92 MiB/s    00:00 ETA
226 OK, received 45397173 bytes
45397173 bytes sent in 00:00 (538.68 MiB/s)
ftp> cd virtual
250 CD worked on /virtual
ftp> ls
229 Entering Extended Passive Mode (|||62369|)
150 Using transfer connection
-rw-rw-rw- 1 ftp ftp         1024 Sep 28 01:44 localpath.txt
-rw-rw-rw- 1 ftp ftp         2048 Sep 28 01:44 file2.txt

226 Closing data connection, sent some bytes
ftp> get localpath.txt
local: localpath.txt remote: localpath.txt
229 Entering Extended Passive Mode (|||62371|)
150 Using transfer connection
    67      241.43 KiB/s 
226 OK, sent 67 bytes
67 bytes received in 00:00 (160.36 KiB/s)
ftp> ^D
221 Goodbye
$ more localpath.txt 
/var/folders/vk/vgsfkf9975xfrc4_fk102g200000gn/T/ftpserver020090599
$ shasum /var/folders/vk/vgsfkf9975xfrc4_fk102g200000gn/T/ftpserver020090599/iMX7D_RM_Rev_B.pdf 
03b3686b31867fb14d3f3a61e20d28a029883a32  /var/folders/vk/vgsfkf9975xfrc4_fk102g200000gn/T/ftpserver020090599/iMX7D_RM_Rev_B.pdf
$ more localpath.txt 
$ shasum iMX7D_RM_Rev_B.pdf 
03b3686b31867fb14d3f3a61e20d28a029883a32  iMX7D_RM_Rev_B.pdf
```

## History of the project

I wanted to make a system which would accept files through FTP and redirect them to something else. Go seemed like the obvious choice and it seemed there was a lot of libraries available but it turns out none of them were in a useable state.

* [micahhausler/go-ftp](https://github.com/micahhausler/go-ftp) is a  minimalistic implementation 
* [shenfeng/ftpd.go](https://github.com/shenfeng/ftpd.go) is very basic and 4 years old.
* [yob/graval](https://github.com/yob/graval) is 3 years old and “experimental”.
* [goftp/server](https://github.com/goftp/server) seemed OK but I couldn't use it on both Filezilla and the MacOs ftp client.
* [andrewarrow/paradise_ftp](https://github.com/andrewarrow/paradise_ftp) - Was the only one of the list I could test right away. This is the project I forked from.

That's why I forked from this last one.
