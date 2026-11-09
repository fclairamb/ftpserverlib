---
model: opus
effort: medium
pr: scan-specs-2026-11-09
automerge: false
---

# One failed Accept of the passive wrapper stops a multiplexed port for good

## Problem
Found by the security scan of 2026-11-09 (finding 2026-11-09-L1).
Severity: Low. Who can trigger it: anyone who can open a TCP connection to the passive port range, without logging
in, on a server with `PassiveTransferPortMultiplexing` and a `MainDriverExtensionPassiveWrapper` whose `Accept`
returns an error for a single connection.

Since aa330d0 (#694), with multiplexing, `WrapPassiveListener` wraps the listener of each shared port once, right
after the bind (`passive_multiplexer.go:139-148`), and the port's accept loop reads from the wrapped listener
(`passive_multiplexer.go:161-187`). That loop retries only when the error is `Temporary()`
(`passive_multiplexer.go:171-176`). Any other error from the wrapper's `Accept` ends the loop:
`failAll` marks the shared listener closed (`passive_multiplexer.go:178-180`, `257-276`) and `serve` returns.

A wrapper can return such an error for one connection, while its listener is still fine. For instance, a PROXY
protocol wrapper (`pires/go-proxyproto`) with a `Policy` that refuses an upstream with an error other than
`ErrInvalidUpstream` returns that error from `Accept`; so does a wrapper that filters peers and reports a refused
peer as an error. One connection from a refused address to a shared port then stops that port:

- no data connection is accepted on it any more;
- `reserve` answers `net.ErrClosed` for it (`passive_multiplexer.go:199-201`) and every later `PASV` skips it
  (`passive_multiplexer.go:52-65`) until the server restarts.

One connection per port of the range is enough to make every multiplexed `PASV` fail with
`could not find any port to listen to`. Without multiplexing, or before aa330d0, the same wrapper error only failed
the transfer whose listener returned it (`transfer_pasv.go:309-315`). SECURITY.md lists exhausting the ports "with a
single connection or a few" as in scope; the trigger depends on the driver's wrapper, hence Low.

A throwaway test reproduced it on 1eaa370: with a wrapper whose `Accept` fails once, one stray TCP connection made
the next `reserve` on that port fail with `ErrNoAvailableListeningPort`.

## Proposal
1. `passive_multiplexer.go` `sharedPassiveListener.serve`: stop only when the shared listener is closing, i.e. when
   `close()` was called or the underlying TCP listener reports a closed error (`isClosedListenerError`,
   `passive_multiplexer.go:423`). Any other `Accept` error from the wrapped listener is logged (`Warn`, with the
   port) and the loop goes on, like a temporary error. Keep a reference to the raw `*net.TCPListener` if that is
   needed to tell the two apart.
2. `driver.go:48-56` (`MainDriverExtensionPassiveWrapper` doc): say that with multiplexing, an error returned by the
   wrapped listener's `Accept` is treated as a refused connection, not as the end of the listener.

## Tests
- `passive_multiplexer_test.go`, regression: a manager whose wrapper returns a listener whose `Accept` fails once
  (with a plain, non-temporary error) and then behaves. Reserve the port for 127.0.0.1, release it, open and close
  one TCP connection to the port, then reserve it again for 127.0.0.1: it must succeed, and a new connection from
  127.0.0.1 must be delivered to the reservation. It fails before the fix (`ErrNoAvailableListeningPort`).
- Positive control: `manager.close()` still ends the accept loop and fails pending reservations
  (`TestPassiveListenersManagerCloseReleasesReservation` keeps passing), and closing the shared listener while a
  reservation waits makes its `Accept` return an error instead of hanging.

## Open questions
- A wrapper whose `Accept` fails on every call (for example after closing itself without closing the TCP listener)
  would make the loop spin. Recommended answer: stop the loop as well when the wrapper's error wraps
  `net.ErrClosed`, and otherwise keep going; a wrapper that fails forever on an open listener is a driver bug.
