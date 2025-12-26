# Switch to log/slog

## Overview

Replace the current logging implementation that uses `github.com/fclairamb/go-log` (a wrapper around multiple logging backends including `github.com/go-kit/log`) with Go's standard library structured logger `log/slog` (introduced in Go 1.21).

## Motivation

- **Reduce dependencies**: Remove the dependency on `github.com/fclairamb/go-log` and `github.com/go-kit/log`
- **Use standard library**: `log/slog` is now part of the Go standard library and provides excellent structured logging
- **Simplify maintenance**: No need to maintain compatibility with multiple logging backends
- **Better performance**: `log/slog` is optimized and well-maintained by the Go team
- **Future-proof**: Standard library APIs have long-term stability guarantees

## Current State

### Dependencies
```
github.com/fclairamb/go-log v0.6.0
github.com/go-kit/log v0.2.1
```

### Usage Pattern
The library currently uses structured logging with key-value pairs:
```go
logger.Debug("Client connected", "clientId", c.id)
logger.Info("Server listening", "address", addr)
logger.Warn("Connection timeout", "duration", timeout)
logger.Error("Network error", "err", err)
```

### Files Using Logger
- `server.go` - Server initialization and lifecycle
- `client_handler.go` - Client connection handling
- `transfer_pasv.go` - Passive transfer handling
- Test files: `driver_test.go`, `client_handler_test.go`, `server_test.go`, etc.

## Proposed Changes

### 1. Logger Interface Migration

**Current import:**
```go
import (
    log "github.com/fclairamb/go-log"
    lognoop "github.com/fclairamb/go-log/noop"
)
```

**New import:**
```go
import (
    "log/slog"
)
```

### 2. API Mapping

The `github.com/fclairamb/go-log` interface closely matches `slog`, but there are some differences:

| Current (go-log)              | New (slog)                    | Notes                          |
|-------------------------------|-------------------------------|--------------------------------|
| `logger.Debug(msg, kvs...)`   | `logger.Debug(msg, kvs...)`   | ✅ Direct mapping              |
| `logger.Info(msg, kvs...)`    | `logger.Info(msg, kvs...)`    | ✅ Direct mapping              |
| `logger.Warn(msg, kvs...)`    | `logger.Warn(msg, kvs...)`    | ✅ Direct mapping              |
| `logger.Error(msg, kvs...)`   | `logger.Error(msg, kvs...)`   | ✅ Direct mapping              |
| `logger.With(kvs...)`         | `logger.With(kvs...)`         | ✅ Direct mapping              |
| `lognoop.NewNoOpLogger()`     | `slog.New(slog.NewTextHandler(io.Discard, nil))` | Create discard logger |

### 3. Driver Interface Changes

The `MainDriver` interface doesn't currently expose a logger, so this is an internal change. However, we should consider if users need to provide a logger.

**Option A: No breaking changes** - Use `slog.Default()` or a package-level logger
**Option B: Add optional interface** - Add `MainDriverExtensionLogger` interface for custom logger injection

Recommendation: **Option A** for simplicity, with **Option B** as a follow-up if needed.

### 4. Code Changes Required

#### Files to Modify

1. **go.mod** - Remove `go-log` and `go-kit/log` dependencies
2. **server.go** - Update imports and logger initialization
3. **client_handler.go** - Update logger calls (mostly already compatible)
4. **transfer_pasv.go** - Update logger calls
5. **Test files** - Update mock loggers and test utilities

#### Example Migration

**Before:**
```go
import (
    log "github.com/fclairamb/go-log"
    lognoop "github.com/fclairamb/go-log/noop"
)

func NewFtpServer(driver MainDriver) (*FtpServer, error) {
    logger := driver.GetLogger() // hypothetical
    if logger == nil {
        logger = lognoop.NewNoOpLogger()
    }
    // ...
}
```

**After:**
```go
import (
    "io"
    "log/slog"
)

func NewFtpServer(driver MainDriver) (*FtpServer, error) {
    logger := slog.Default()
    // Or for no-op: slog.New(slog.NewTextHandler(io.Discard, nil))
    // ...
}
```

### 5. Logger Configuration

Users of the library can configure the global slog logger before creating the FTP server:

```go
// Example: JSON structured logging
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
})
slog.SetDefault(slog.New(handler))

// Then create FTP server
server, err := ftpserver.NewFtpServer(driver)
```

Alternatively, we could add a method to inject a custom logger:

```go
server.SetLogger(customLogger)
```

## Migration Strategy

### Phase 1: Internal Changes (Non-breaking)
1. Update imports in all source files
2. Replace `lognoop.NewNoOpLogger()` with `slog.New(slog.NewTextHandler(io.Discard, nil))`
3. Update logger initialization to use `slog.Default()`
4. Run all tests to verify compatibility

### Phase 2: Dependency Cleanup
1. Update `go.mod` to remove `go-log` and `go-kit/log`
2. Run `go mod tidy`
3. Verify build and tests

### Phase 3: Documentation
1. Update README.md to remove references to `go-log`
2. Add examples of configuring slog
3. Update CHANGELOG.md

### Phase 4: Optional Enhancements
1. Consider adding `MainDriverExtensionLogger` interface for custom logger injection
2. Add convenience methods for common logging patterns

## Testing Strategy

### Unit Tests
- Verify all logging calls work with slog
- Test with different slog handlers (Text, JSON, Discard)
- Ensure no panics or errors in logging code

### Integration Tests
- Run existing test suite with slog
- Verify log output format is acceptable
- Test with custom slog configuration

### Mock Logger for Tests
Create a test helper that captures slog output:

```go
type TestLogHandler struct {
    Logs []TestLogRecord
}

type TestLogRecord struct {
    Level   slog.Level
    Message string
    Attrs   map[string]any
}

func (h *TestLogHandler) Handle(ctx context.Context, r slog.Record) error {
    attrs := make(map[string]any)
    r.Attrs(func(a slog.Attr) bool {
        attrs[a.Key] = a.Value.Any()
        return true
    })
    h.Logs = append(h.Logs, TestLogRecord{
        Level:   r.Level,
        Message: r.Message,
        Attrs:   attrs,
    })
    return nil
}
```

## Backward Compatibility

### Breaking Changes
**None expected** - This is an internal implementation change. The library's public API doesn't expose the logger type.

### For Library Users
Users currently don't interact with the logger directly. They would need to:
- Configure `slog` globally if they want custom logging (instead of using the `go-log` adapter)
- This is actually **simpler** for users who just want to use a standard logger

### For Contributors
- Code using `log.Debug/Info/Warn/Error` will continue to work with minimal changes
- The key-value pair syntax is identical between `go-log` and `slog`

## Risks and Mitigation

### Risk 1: Missing Logger Methods
**Mitigation**: Comprehensive grep of all logger method calls to ensure compatibility

### Risk 2: Performance Changes
**Mitigation**: Benchmark critical paths before and after migration

### Risk 3: Log Format Changes
**Mitigation**:
- Users can configure slog handlers to match desired format
- Provide examples for common formats (JSON, text)

## Implementation Checklist

- [ ] Search and catalog all logger usage in the codebase
- [ ] Update `server.go` imports and logger initialization
- [ ] Update `client_handler.go` logger calls
- [ ] Update `transfer_pasv.go` logger calls
- [ ] Update all test files with new mock logger
- [ ] Replace `lognoop` with slog discard handler
- [ ] Remove `go-log` and `go-kit/log` from go.mod
- [ ] Run full test suite
- [ ] Run linter (golangci-lint)
- [ ] Update README.md
- [ ] Update examples (if any)
- [ ] Update CHANGELOG.md
- [ ] Verify documentation builds correctly

## Success Criteria

- ✅ All tests pass with slog
- ✅ No linter errors
- ✅ Zero external logging dependencies (except stdlib)
- ✅ Code coverage remains at ~92%+
- ✅ Documentation updated
- ✅ No breaking changes to public API

## Timeline

Estimated effort: **2-4 hours**
- Analysis and planning: 30 min (done)
- Implementation: 1-2 hours
- Testing: 30-60 min
- Documentation: 30 min

## References

- [Go slog documentation](https://pkg.go.dev/log/slog)
- [slog design proposal](https://go.googlesource.com/proposal/+/master/design/56345-structured-logging.md)
- [Current go-log library](https://github.com/fclairamb/go-log)
