# Deflate Mode (MODE Z) Implementation

## Overview

Implement FTP transfer compression using the `MODE Z` command (deflate mode) as specified in [draft-preston-ftpext-deflate-04](https://datatracker.ietf.org/doc/html/draft-preston-ftpext-deflate-04). This feature allows clients to request compressed data transfers, reducing bandwidth usage for compressible content.

## Current State

### What's Already Implemented

1. **`TransferMode` enum** (`client_handler.go:37-43`):
   ```go
   type TransferMode int8
   const (
       TransferModeStream  TransferMode = iota
       TransferModeDeflate
   )
   ```

2. **`transferMode` field** in `clientHandler` struct (`client_handler.go:115`)

3. **`MODE` command handler** (`handle_misc.go:297-310`):
   ```go
   func (c *clientHandler) handleMODE(param string) error {
       switch param {
       case "S":
           c.transferMode = TransferModeStream
           c.writeMessage(StatusOK, "Using stream mode")
       case "Z":
           c.transferMode = TransferModeDeflate
           c.writeMessage(StatusOK, "Using deflate mode")
       default:
           c.writeMessage(StatusNotImplementedParam, "Unsupported mode")
       }
       return nil
   }
   ```

4. **`deflateReadWriter` struct** (`client_handler.go:873-890`):
   ```go
   type deflateReadWriter struct {
       io.Reader
       *flate.Writer
   }

   func newDeflateTransfer(conn io.ReadWriter, level int) (io.ReadWriter, error) {
       writer, err := flate.NewWriter(conn, level)
       reader := flate.NewReader(conn)
       return &deflateReadWriter{Reader: reader, Writer: writer}, nil
   }
   ```

5. **`DeflateCompressionLevel` setting** in `Settings` struct (`driver.go:326`)

6. **Deflate wrapper in `TransferOpen`** (`client_handler.go:721-728`):
   ```go
   if c.transferMode == TransferModeDeflate {
       transferStream, err = newDeflateTransfer(transferStream, c.server.settings.DeflateCompressionLevel)
       // ...
   }
   ```

7. **`TransferClose` flushing** (`client_handler.go:754-761`):
   ```go
   if flush, ok := transfer.(Flusher); ok {
       if errFlush := flush.Flush(); errFlush != nil {
           // log error
       }
   }
   ```

### The Problem

The current implementation has a critical bug that causes `unexpected EOF` errors during both upload and download operations. This was identified 2 years ago:

> "I have created a deflateConn struct that is designed to be used as a net.Conn but the original net.Conn reference is kept in the passive/active transfer handlers. Which makes us not flush the data at the end of the transfer. This is why download doesn't work."

**Root Cause Analysis:**

The issue is that `flate.Writer.Flush()` only flushes buffered data to the underlying writer - it does **not** write the end-of-stream marker (BFINAL=1 block in DEFLATE format). To properly terminate a deflate stream, you must call `Close()` on the `flate.Writer`.

The problem is that when `TransferClose` calls `Flush()`, it:
1. Flushes buffered compressed data
2. But leaves the deflate stream incomplete (no BFINAL block)
3. Then immediately closes the underlying connection

The receiving side's `flate.Reader` then tries to read more data (expecting the BFINAL block), but the connection is already closed, resulting in `unexpected EOF`.

### Test Evidence

Running `TestTransferModeDeflate` produces:
```
550 Issue during transfer: network error: error transferring data: unexpected EOF
```

This confirms the deflate stream is not properly terminated before the connection closes.

## Proposed Solution

### Phase 1: Fix the Stream Termination Issue

The `deflateReadWriter` needs a proper `Close()` method that:
1. Calls `Close()` on the underlying `flate.Writer` to write the BFINAL block
2. Does NOT close the underlying connection (that's handled by the transfer handler)

**Proposed changes to `client_handler.go`:**

```go
// Closer is the interface for types that need to be closed to finalize data
type Closer interface {
    Close() error
}

type deflateReadWriter struct {
    reader io.ReadCloser  // flate.Reader implements io.ReadCloser
    *flate.Writer
}

func (d *deflateReadWriter) Read(p []byte) (int, error) {
    return d.reader.Read(p)
}

// Close finalizes the deflate stream by writing the BFINAL block.
// This does NOT close the underlying connection.
func (d *deflateReadWriter) Close() error {
    return d.Writer.Close()
}

func newDeflateTransfer(conn io.ReadWriter, level int) (*deflateReadWriter, error) {
    writer, err := flate.NewWriter(conn, level)
    if err != nil {
        return nil, fmt.Errorf("could not create deflate writer: %w", err)
    }

    reader := flate.NewReader(conn)

    return &deflateReadWriter{
        reader: reader,
        Writer: writer,
    }, nil
}
```

**Update `TransferClose` to call Close before Flush:**

```go
func (c *clientHandler) TransferClose(transfer io.ReadWriter, err error) {
    c.transferMu.Lock()
    defer c.transferMu.Unlock()

    // First, close the deflate stream to write BFINAL block (if applicable)
    if closer, ok := transfer.(Closer); ok {
        if errClose := closer.Close(); errClose != nil {
            c.logger.Warn(
                "Error closing transfer stream",
                "err", errClose,
            )
        }
    }

    // Then flush any remaining data
    if flush, ok := transfer.(Flusher); ok {
        if errFlush := flush.Flush(); errFlush != nil {
            c.logger.Warn(
                "Error flushing transfer connection",
                "err", errFlush,
            )
        }
    }

    // Finally close the underlying connection
    errClose := c.closeTransfer()
    // ... rest of the function
}
```

### Phase 2: Advertise MODE Z in FEAT Response

Add `MODE Z` to the FEAT response so clients know deflate is supported:

**Update `handleFEAT` in `handle_misc.go`:**

```go
func (c *clientHandler) handleFEAT(_ string) error {
    // ... existing code ...

    features := []string{
        // ... existing features ...
    }

    // Add MODE Z support
    features = append(features, "MODE Z")

    // ... rest of function
}
```

### Phase 3: Handle Bidirectional Compression

For transfers where both upload and download need compression (e.g., LIST, MLSD), ensure the deflate reader is also properly handled:

- For **uploads** (STOR, APPE): Server reads from deflate reader, writes to file
- For **downloads** (RETR, LIST, NLST, MLSD): Server writes to deflate writer, client reads

The current implementation handles this correctly since `deflateReadWriter` embeds both a reader and writer. The fix in Phase 1 addresses the main issue.

### Phase 4: Configuration and Edge Cases

1. **Compression Level**: Already configurable via `DeflateCompressionLevel` in Settings (default should be 5-6 for balance between compression and CPU)

2. **Error Handling**: Ensure deflate errors don't leak to client in a confusing way

3. **ASCII Mode Interaction**: MODE Z should work with both TYPE A and TYPE I. The compression happens at the transport layer, after any ASCII conversion.

4. **REST Command**: Resume with deflate may be problematic since deflate is a streaming compression. Consider:
   - Disallow REST with MODE Z, OR
   - Reset to byte offset in the uncompressed stream (complex)

## Implementation Checklist

- [ ] Update `deflateReadWriter` with proper `Close()` method
- [ ] Update `TransferClose` to call `Close()` before closing connection
- [ ] Add MODE Z to FEAT response
- [ ] Set default `DeflateCompressionLevel` to 5 if not configured
- [ ] Add tests for:
  - [ ] Upload with deflate mode
  - [ ] Download with deflate mode
  - [ ] MODE Z followed by MODE S (switch back to stream)
  - [ ] Deflate with ASCII mode (TYPE A + MODE Z)
  - [ ] Large file transfers with deflate
  - [ ] Error handling (connection drops during deflate transfer)
- [ ] Consider REST + MODE Z interaction
- [ ] Update documentation

## Testing Strategy

### Unit Tests

Expand `TestTransferModeDeflate` to cover:
1. Upload a file with MODE Z, verify content matches
2. Download the file with MODE Z, verify content matches
3. Switch between MODE Z and MODE S
4. Test with various compression levels

### Integration Tests

1. Test with real FTP clients that support MODE Z (e.g., lftp)
2. Test interoperability with other FTP servers

## Security Considerations

- **Compression bombs**: Consider limiting the decompression ratio or the expanded size
- **CPU exhaustion**: Compression level 9 is CPU-intensive; consider recommending level 5-6

## References

- [draft-preston-ftpext-deflate-04](https://datatracker.ietf.org/doc/html/draft-preston-ftpext-deflate-04)
- [RFC 1951 - DEFLATE Compressed Data Format](https://www.rfc-editor.org/rfc/rfc1951)
- [Go compress/flate documentation](https://pkg.go.dev/compress/flate)

## Timeline

Estimated effort: **4-6 hours**
- Phase 1 (Critical fix): 1-2 hours
- Phase 2 (FEAT): 15 minutes
- Phase 3 (Verification): 30 minutes
- Phase 4 (Configuration): 1 hour
- Testing: 1-2 hours
- Documentation: 30 minutes
