# SITE Commands Interface

This document describes the interface-based implementation of SITE commands in ftpserverlib.

## Overview

SITE commands are now implemented using an interface-based approach that allows for:
1. Custom implementations through the `ClientDriverExtensionSiteCommand` interface
2. Fallback to default implementations when the interface is not implemented
3. Consistent behavior with existing extension patterns in the library

## Interface Definition

```go
// ClientDriverExtensionSiteCommand is an extension to support SITE commands
type ClientDriverExtensionSiteCommand interface {
    // SiteChmod handles the "SITE CHMOD" command
    SiteChmod(path string, mode os.FileMode) error

    // SiteChown handles the "SITE CHOWN" command  
    SiteChown(path string, uid, gid int) error

    // SiteMkdir handles the "SITE MKDIR" command
    SiteMkdir(path string) error

    // SiteRmdir handles the "SITE RMDIR" command
    SiteRmdir(path string) error
}
```

## Supported SITE Commands

### SITE CHMOD
- **Syntax**: `SITE CHMOD <mode> <path>`
- **Extension method**: `SiteChmod(path string, mode os.FileMode) error`
- **Fallback**: Uses `afero.Fs.Chmod(path, mode)`

### SITE CHOWN
- **Syntax**: `SITE CHOWN <uid>[:<gid>] <path>`
- **Extension method**: `SiteChown(path string, uid, gid int) error`
- **Fallback**: Uses `afero.Fs.Chown(path, uid, gid)`

### SITE MKDIR
- **Syntax**: `SITE MKDIR <path>`
- **Extension method**: `SiteMkdir(path string) error`
- **Fallback**: Uses `afero.Fs.MkdirAll(path, 0o755)`

### SITE RMDIR
- **Syntax**: `SITE RMDIR <path>`
- **Extension method**: `SiteRmdir(path string) error`
- **Fallback**: Uses `afero.Fs.RemoveAll(path)`

### SITE SYMLINK
- **Syntax**: `SITE SYMLINK <oldname> <newname>`
- **Extension**: Uses existing `ClientDriverExtensionSymlink` interface
- **No fallback**: Returns error if not implemented

## Implementation Example

```go
type MyClientDriver struct {
    afero.Fs
}

// Implement the SITE command extension
func (d *MyClientDriver) SiteChmod(path string, mode os.FileMode) error {
    // Custom chmod implementation
    log.Printf("Custom CHMOD: %s -> %o", path, mode)
    return d.Fs.Chmod(path, mode)
}

func (d *MyClientDriver) SiteChown(path string, uid, gid int) error {
    // Custom chown implementation with validation
    if uid < 0 || gid < 0 {
        return errors.New("invalid uid or gid")
    }
    log.Printf("Custom CHOWN: %s -> %d:%d", path, uid, gid)
    return d.Fs.Chown(path, uid, gid)
}

func (d *MyClientDriver) SiteMkdir(path string) error {
    // Custom mkdir implementation
    log.Printf("Custom MKDIR: %s", path)
    return d.Fs.MkdirAll(path, 0o755)
}

func (d *MyClientDriver) SiteRmdir(path string) error {
    // Custom rmdir implementation
    log.Printf("Custom RMDIR: %s", path)
    return d.Fs.RemoveAll(path)
}
```

## Behavior

1. **With Extension**: When a client driver implements `ClientDriverExtensionSiteCommand`, the extension methods are called for SITE commands.

2. **Without Extension**: When a client driver does not implement the interface, the server falls back to using the standard `afero.Fs` methods.

3. **Error Handling**: Both extension and fallback implementations return appropriate FTP status codes based on the operation result.

## Testing

The implementation includes comprehensive tests that verify:
- Extension-based behavior
- Fallback behavior
- Error handling
- Parameter validation
- Disabled SITE commands

## Migration

This change is backward compatible. Existing drivers will continue to work using the fallback implementations, while new drivers can implement the interface for custom behavior.