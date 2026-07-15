# Optional extensions

Implement extension interfaces only for behavior your storage layer supports.
The core library detects these interfaces at runtime.

- `ClientDriverExtensionAllocate` reserves upload space for `ALLO`.
- `ClientDriverExtensionAvailableSpace` reports space for `AVBL`.
- `ClientDriverExtensionSymlink` adds symbolic-link support.
- `ClientDriverExtensionHasher` computes digests when `EnableHASH` is set.
- `FileTransferError` receives detected abort, disconnect, and copy failures
  before the underlying file is closed.

FTP uploads do not carry a universal length header, so a server cannot detect
every truncated upload. The transfer-error hook reports failures the protocol
engine can observe; applications that need end-to-end completeness should add
their own size, digest, or transaction checks.

See the generated API reference for the remaining extension interfaces and
their exact method signatures.
