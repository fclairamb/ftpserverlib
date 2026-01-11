# CLAUDE.md

This file provides guidance for AI assistants working with the ftpserverlib codebase.

## Project Overview

**ftpserverlib** is a Go library for building FTP servers using [afero](https://github.com/spf13/afero) as the backend filesystem. It implements RFC 959 and numerous extensions, providing a clean, driver-based architecture for customization.

**Repository**: `github.com/fclairamb/ftpserverlib`

## Build and Test Commands

```bash
# Build
go build -v ./...

# Run tests (standard)
go test -v ./...

# Run tests with race detection (as CI does)
go test -parallel 20 -v -race -coverprofile=coverage.txt -covermode=atomic ./...

# Run linter (requires golangci-lint v2.4.0+)
golangci-lint run

# Format code
gofmt -w .
goimports -w -local github.com/fclairamb/ftpserverlib .
```

## Project Architecture

### Single Package Design

The entire library is a single `ftpserver` package with files organized by responsibility:

| File(s) | Purpose |
|---------|---------|
| `server.go` | Main `FtpServer` struct, initialization, listener management |
| `client_handler.go` | Per-client connection state machine and command parsing |
| `handle_auth.go` | USER, PASS, AUTH, PROT, PBSZ commands |
| `handle_dirs.go` | CWD, CDUP, MKD, RMD, PWD commands |
| `handle_files.go` | STOR, RETR, LIST, NLST, MLST, MLSD, DELE, SIZE, etc. |
| `handle_misc.go` | SYST, FEAT, NOOP, QUIT, SITE, STAT, HELP commands |
| `transfer_pasv.go` | Passive mode data connections (PASV, EPSV) |
| `transfer_active.go` | Active mode data connections (PORT, EPRT) |
| `driver.go` | All interface definitions (MainDriver, ClientDriver, extensions) |
| `consts.go` | FTP status codes and constants |
| `errors.go` | Custom error types (DriverError, NetworkError, FileAccessError) |
| `asciiconverter.go` | ASCII mode CRLF/LF conversion |

### Driver-Based Architecture

Users implement interfaces to customize server behavior:

```go
// Required: Main authentication and configuration
type MainDriver interface {
    GetSettings() (*Settings, error)
    ClientConnected(cc ClientContext) (string, error)
    ClientDisconnected(cc ClientContext)
    AuthUser(cc ClientContext, user, pass string) (ClientDriver, error)
    GetTLSConfig() (*tls.Config, error)
}

// Required: Filesystem operations (wraps afero.Fs)
type ClientDriver interface {
    afero.Fs
}
```

### Extension Pattern

Optional features use interface assertion:

```go
// In handler code:
if hasher, ok := c.driver.(ClientDriverExtensionHasher); ok {
    // Extension is supported, use it
    hash, err := hasher.ComputeHash(name, algo, start, end)
}
```

Available extensions:
- `MainDriverExtensionTLSVerifier` - TLS certificate authentication
- `MainDriverExtensionUserVerifier` - Pre-auth user validation
- `MainDriverExtensionPostAuthMessage` - Custom post-auth messages
- `MainDriverExtensionPassiveWrapper` - Wrap passive listeners
- `MainDriverExtensionQuitMessage` - Custom quit messages
- `ClientDriverExtensionAllocate` - ALLO command support
- `ClientDriverExtensionSymlink` - SITE SYMLINK support
- `ClientDriverExtensionFileList` - Custom directory listing
- `ClientDriverExtentionFileTransfer` - Custom file transfer handles
- `ClientDriverExtensionRemoveDir` - Distinguish RMD from DELE
- `ClientDriverExtensionHasher` - Custom hash implementations
- `ClientDriverExtensionAvailableSpace` - AVBL command support
- `ClientDriverExtensionSite` - Custom SITE subcommands

## Coding Conventions

### Naming
- Exported: `PascalCase` (e.g., `FtpServer`, `MainDriver`, `Settings`)
- Unexported: `camelCase` (e.g., `clientHandler`, `transferHandler`)
- Command handlers: `handle{COMMAND}` (e.g., `handleUSER`, `handleRETR`)
- Test files: `*_test.go` colocated with implementation

### Enums
Use `type X int8` with `iota`:
```go
type HASHAlgo int8
const (
    HASHAlgoCRC32 HASHAlgo = iota
    HASHAlgoMD5
    HASHAlgoSHA1
    HASHAlgoSHA256
    HASHAlgoSHA512
)
```

### Error Handling
- Use custom error types: `DriverError`, `NetworkError`, `FileAccessError`
- Wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Check errors with `errors.Is()`: `errors.Is(err, ErrStorageExceeded)`
- Use `getErrorCode()` to map Go errors to FTP status codes

### Synchronization
- No global mutexes (only per-client)
- `paramsMutex` (RWMutex) protects public API fields in `clientHandler`
- `transferMu` protects transfer connection state
- `sync.WaitGroup` for command-to-transfer coordination

### Design Principles
- **No sleep**: Use proper synchronization, not time delays
- **No panic**: Propagate errors, don't crash
- **No global sync**: Each client manages its own state

## Linter Configuration

The project uses golangci-lint v2 with strict settings (`.golangci.yml`):

- **Line length**: 120 characters max
- **Function length**: 80 lines / 40 statements max
- **Cyclomatic complexity**: 15 max
- **Cognitive complexity**: 30 max
- **Import organization**: stdlib, third-party, then local (`github.com/fclairamb/ftpserverlib`)

Key enabled linters: `gosec`, `errcheck`, `errorlint`, `gocyclo`, `gocognit`, `funlen`, `dupl`, `unparam`, `staticcheck`

## Testing

### Test Infrastructure
- Tests use `github.com/stretchr/testify` (both `assert` and `require`)
- Reference driver implementation in `driver_test.go` (`TestServerDriver`)
- Setup helpers: `NewTestServer()`, `NewTestServerWithTestDriver()`, `NewTestServerWithDriver()`
- FTP client for integration tests: `github.com/secsy/goftp` (replaced with fork)

### Running Tests
```bash
# Standard test run
go test -v ./...

# With race detection (recommended)
go test -race ./...

# Specific test
go test -v -run TestNamePattern ./...
```

### Test Patterns
- Table-driven tests for multiple scenarios
- Real filesystem via `afero.NewBasePathFs` with temp directories
- Integration tests using actual FTP protocol
- Concurrent client testing (100+ simultaneous connections)

## Dependencies

**Go Version**: 1.24.0 minimum, toolchain 1.25.5

**Direct Dependencies**:
- `github.com/spf13/afero` - Filesystem abstraction
- `github.com/fclairamb/go-log` - Logging abstraction (supports go-kit, logrus, zap, zerolog)
- `golang.org/x/sys` - Platform syscalls

**Test Dependencies**:
- `github.com/stretchr/testify` - Assertions
- `github.com/secsy/goftp` (replaced with `github.com/drakkan/goftp`) - FTP client
- `github.com/go-kit/log` - Default logging backend for tests

## Common Tasks

### Adding a New FTP Command

1. Add handler method in appropriate `handle_*.go` file:
   ```go
   func (c *clientHandler) handleNEWCMD(param string) error {
       // Implementation
       return c.writeMessage(StatusOK, "Command successful")
   }
   ```

2. Register in `commandsMap` in `consts.go`:
   ```go
   "NEWCMD": {Fn: (*clientHandler).handleNEWCMD, Open: false, TransferRelated: false},
   ```

3. Add to FEAT response if applicable (in `handleFEAT`)

4. Write tests in corresponding `handle_*_test.go`

### Adding a Driver Extension

1. Define interface in `driver.go`:
   ```go
   type ClientDriverExtensionNewFeature interface {
       NewFeatureMethod(args) (result, error)
   }
   ```

2. Check for extension in handler:
   ```go
   if ext, ok := c.driver.(ClientDriverExtensionNewFeature); ok {
       result, err := ext.NewFeatureMethod(args)
   }
   ```

### Modifying Server Settings

Settings are defined in `driver.go` (`Settings` struct) and returned via `MainDriver.GetSettings()`. Add new fields there and handle them appropriately in server/client code.

## CI/CD

GitHub Actions workflow (`.github/workflows/build.yml`):
- Runs on: `ubuntu-24.04`
- Go versions: 1.25 (with linting), 1.24
- Steps: Lint -> Build -> Test (with race detection) -> Codecov upload

## Key Files Reference

- `driver.go` - All public interfaces
- `server.go` - Server initialization and lifecycle
- `client_handler.go` - Client state machine (largest file)
- `consts.go` - FTP status codes and command registration
- `driver_test.go` - Reference driver implementation for testing
