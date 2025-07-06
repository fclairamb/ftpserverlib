# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **Go FTP server library** (`ftpserver` package) that provides a complete RFC 959-compliant FTP server implementation with TLS, IPv6, and extended command support. The library uses a **driver-based architecture** where users implement interfaces to customize file system operations and authentication.

## Architecture

### Core Driver Pattern
The library centers around three main interfaces:
- **MainDriver**: Authentication, client lifecycle, TLS configuration
- **ClientDriver**: File system operations (based on `afero.Fs`)  
- **ClientContext**: Client connection metadata and context

### Key Components
- **Server Core** (`server.go`): Main server with command mapping system
- **Client Handler** (`client_handler.go`): Per-client connection management and protocol state machine
- **Command Handlers**: Organized by functionality (`handle_*.go` files)
- **Transfer System**: Separate active (`transfer_active.go`) and passive (`transfer_pasv.go`) mode implementations

## Common Commands

### Development
```bash
# Build the library
go build -v ./...

# Run full test suite with race detection and coverage
go test -parallel 20 -v -race -coverprofile=coverage.txt -covermode=atomic ./...

# Lint code
golangci-lint run
```

### Testing Specific Components
```bash
# Test individual handlers
go test -v -run TestHandle

# Test transfers specifically  
go test -v -run TestTransfer

# Run benchmarks
go test -bench=.
```

## File Organization

### Command Handler Structure
- `handle_auth.go`: Authentication commands (USER, PASS, AUTH, PBSZ, PROT)
- `handle_files.go`: File operations (STOR, RETR, LIST, NLST, MLST, MLSD)
- `handle_dirs.go`: Directory operations (CWD, CDUP, MKD, RMD, PWD)
- `handle_misc.go`: System commands (SYST, FEAT, NOOP, QUIT, HELP)

### Core Files
- `driver.go`: Interface definitions and driver extensions
- `client_handler.go`: Main protocol implementation and state management
- `server.go`: Server initialization and command routing
- `errors.go`: FTP-specific error codes and handling

## Testing Architecture

The test suite uses a **reference driver implementation** (`driver_test.go`) with:
- Mock file system using `afero.NewBasePathFs` with temp directories
- Integration tests that simulate real FTP client interactions
- Comprehensive coverage of all command handlers and transfer modes
- Race condition testing for concurrent operations

## Key Dependencies

- `github.com/spf13/afero`: File system abstraction for driver implementations
- `github.com/fclairamb/go-log`: Logging abstraction supporting multiple frameworks

## Code Conventions

- **No global state**: All server instances are isolated
- **Interface-based design**: Extensive use of optional interfaces for extensibility
- **Error handling**: Custom FTP error types with appropriate status codes
- **Concurrency**: Clean goroutine management without sleep/panic patterns
- **Line length**: 120 characters maximum (enforced by linter)
- **Function length**: 80 lines maximum (enforced by linter)