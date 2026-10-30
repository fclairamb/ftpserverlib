---
model: opus
effort: medium
pr: scan-specs-2026-10-30
automerge: false
---

# PORT/EPRT and PASV error paths drop passive listeners without closing them

## Problem
Found by the security scan of 2026-10-30 (finding 2026-10-30-M2).
Severity: Medium. Who can trigger it: any authenticated user (and, for the IPv6 case, any authenticated client
sending PASV on an IPv6 control connection).

- `handlePORT` (`transfer_active.go:65-74`, also used for EPRT) replaces `c.transfer` with a new
  `activeTransferHandler` without closing the previous handler. `handlePASV` does close it
  (`transfer_pasv.go:204-207`). After `PASV`, a `PORT` leaves the passive listener bound.
- `handlePASV` (`transfer_pasv.go:150-213`) returns early without closing the listener it has just created:
  - wrapper error (`transfer_pasv.go:161-168`): the original listener is lost, and `listener` may even be nil;
  - `GetTLSConfig` error (`transfer_pasv.go:171-178`);
  - `handlePassivePASV` error (`transfer_pasv.go:196-199`, `246-255`). `getCurrentIP` fails, for example, for a
    plain `PASV` over an IPv6 control connection without `PublicHost`
    (`transfer_pasv.go:65-75`: the split on `:` does not give 4 IPv4 quads).

A user looping PASV then PORT leaks one bound listener per loop until the GC finalizer closes the descriptor.
With a `PassiveTransferPortRange`, the range stays exhausted and every other user's PASV fails with
`ErrNoAvailableListeningPort`. With `PassiveTransferPortMultiplexing`, the leaked `passiveReservationListener` is
still referenced from `sharedPassiveListener.reservations` (`passive_multiplexer.go:200`), so it is never
collected and the (port, client IP) slot stays taken.

## Proposal
1. `transfer_active.go` `handlePORT`: under `transferMu`, close the previous `c.transfer` (if any) before assigning
   the new handler, as `handlePASV` does.
2. `transfer_pasv.go` `handlePASV`: on each early return after the listener was obtained, close it. On a wrapper
   error, close the original listener (keep a reference to it before wrapping). A small `defer` that closes
   unless the handler was installed keeps this readable and within the funlen limit.
3. Optional and separate: `getCurrentIP` could fall back to an error message telling the client to use EPSV on
   IPv6. Not required for this fix.

## Tests
- `transfer_test.go`: with a one-port `PassiveTransferPortRange`, PASV, then PORT to the client's own address,
  then PASV again. The second PASV succeeds. Fails before the fix (the port is still bound).
- `passive_multiplexer_test.go`: with multiplexing on, PASV then PORT. The reservation of that port for the client
  IP is released, and a following PASV gets the same port.
- `transfer_test.go`: a driver whose `WrapPassiveListener` returns an error, with a one-port range. Two PASV in a
  row both answer the wrapper error, and the port is free afterwards (not "could not find any port").
- Positive control: PASV then RETR, and PORT then RETR, still transfer, and the existing transfer tests pass.
