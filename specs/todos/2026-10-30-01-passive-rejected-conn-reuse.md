---
model: opus
effort: medium
pr: scan-specs-2026-10-30
automerge: false
---

# A passive data connection rejected by the IP check is reused by the next transfer

## Problem
Found by the security scan of 2026-10-30 (finding 2026-10-30-M1).
Severity: Medium. Who can trigger it: a network peer that can reach the passive port, from an IP other than the
client's. It needs a client that re-issues a transfer command after a 425 without a new PASV/EPSV.

`passiveTransferHandler.ConnectionWait` (`transfer_pasv.go:269-302`) stores the accepted connection in
`p.connection` (`transfer_pasv.go:282`) **before** the data-connection requirement check
(`transfer_pasv.go:294`). When the check fails (default `PasvConnectionsCheck = IPMatchRequired`), it returns
an error but keeps the rejected connection in `p.connection`, open. The next call returns it straight away
(`transfer_pasv.go:270-272`), and nothing checks it again.

`TransferOpen` (`client_handler.go:706-777`) leaves `c.transfer` in place when `Open()` fails
(`client_handler.go:729-737`) or when the protection does not match (`client_handler.go:744-752`). None of its
callers close it (`handle_files.go:101-111`, `handle_dirs.go:192`, `211`, `258`). The same handler, holding the
rejected connection, therefore serves the next RETR/STOR/LIST of the session. Downloads then go to that peer and
uploads are read from it, with `PROT P` too, since that peer can complete the TLS handshake like any client. This
defeats the IP-match protection. Port multiplexing (`passive_multiplexer.go`) is not affected: it dispatches by IP.

The existing `TestPASVConnectionWait` (`transfer_test.go:1313`) succeeds on its second call only because of
this caching: the second call returns the first, invalid connection.

The new `errProtMismatch` path of c7715c4 has the same shape: the connection it refuses stays open and cached
until the next PASV/PORT or the end of the session.

## Proposal
1. `transfer_pasv.go` `ConnectionWait`: accept into a local variable. When the IP cannot be parsed or the
   requirement check fails, close that connection, log it, and keep accepting until the deadline set at
   `transfer_pasv.go:275-277`, so that a peer racing the client cannot block the transfer either. On timeout,
   return the existing error. Set `p.connection` only once a connection has passed the check.
2. `client_handler.go` `TransferOpen`: on every failure after `c.transfer.Open()` was called (Open error,
   `errProtMismatch`, deflate setup error at `client_handler.go:757-763`), call `c.closeTransfer()` (we already
   hold `transferMu`) so that no half-open handler survives. The client must send PASV/PORT again, which is what
   RFC 959 clients do after a 425.
3. Fix `TestPASVConnectionWait` (`transfer_test.go:1313-1361`) so that it no longer relies on the caching.

## Tests
- `transfer_test.go`: unit test on `passiveTransferHandler`, with a listener that yields first a connection from a
  non-matching IP, then one from the control IP. `ConnectionWait` returns the second, and the first is closed.
  Fails before the fix: the first call errors and a second call returns the rejected connection.
- `transfer_test.go`: a listener that only yields non-matching connections. `ConnectionWait` times out with an
  error, every connection is closed, and a second call does not return any of them.
- Integration (`transfer_test.go`): PASV, then RETR fails with 425 (for example a refused protection, or an
  `Open` error). A second RETR without a new PASV must answer an error (no transfer handler), not reuse the
  connection.
- Positive control: PASV then RETR from the control IP still works, and the multiplexed passive mode tests still
  pass.

## To verify
- Whether `testNetListener` (`transfer_test.go`) can yield a sequence of connections. If not, extend it.
