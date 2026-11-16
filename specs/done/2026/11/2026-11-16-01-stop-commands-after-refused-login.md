---
model: opus
effort: medium
pr: scan-specs-2026-11-16
automerge: false
---

# Commands already sent still run after a refused login closed the control connection

## Problem
Found by the security scan of 2026-11-16 (finding 2026-11-16-L1).
Severity: Low. Who can trigger it: a client that is not logged in, on a server whose `Settings.IdleTimeout` is
negative (idle timeout disabled), and whose `MainDriver.AuthUser` returns a non-nil `ClientDriver` together with an
error.

Two flaws combine:

1. `handlePASS` stores whatever `AuthUser` returns before looking at the error:
   `c.driver, err = c.server.driver.AuthUser(c, c.user, param)` (`handle_auth.go:95`). On an error it answers 530
   and closes the connection (`handle_auth.go:106-112`), but `c.driver` keeps the driver returned with the error.
   Go's convention is that the other results are meaningless when `err != nil`, so a driver may well return a
   partially built file system with its error (e.g. it builds the user's `afero.Fs` first and checks the password
   or the account status afterwards).
2. `disconnect()` only closes the connection (`client_handler.go:152-174`). The command loop goes on reading
   (`client_handler.go:459-497`): lines already in the `bufio.Reader` (up to `maxCommandSize`, 4 KB) are read from
   the buffer without touching the closed connection. The loop only notices the closed connection through
   `setIdleDeadline()` (`client_handler.go:469`), which is skipped when `IdleTimeout <= 0` once `NewFtpServer`'s
   default has been applied (`server.go:177-179` only replaces 0).

So, with the idle timeout disabled, a client that sends `USER`, a wrong `PASS` and more commands in one write gets
its 530, and the following commands run anyway. `handleCommand`'s login check (`client_handler.go:604`) only tests
`c.driver == nil`, so with the driver of flaw 1 they run as that user: `MKD`, `DELE`, `RNFR`/`RNTO`, `SITE`...
Replies can't be sent (the connection is closed), but the changes happen. Reproduced in a scratch copy: a driver
returning `(afero.NewMemMapFs(), err)`, `IdleTimeout: -1`, and `MKD` sent after a refused `PASS` created the
directory. With the default idle timeout, the commands are not run.

Even with a driver that returns `nil` on error, the open commands (`USER`, `PASS`, `FEAT`...) of that buffer are
still run after the disconnect, so the disconnect on a refused login does not stop the next password attempts of
the same write.

SECURITY.md scope: "run a command or reach a file before a successful login".

## Proposal
1. `handle_auth.go:95-113`: keep the driver only on success. Assign `AuthUser`'s result to a local variable, and set
   `c.driver` to it only when `err == nil` and it isn't nil. On any failure, set `c.driver = nil` (a re-login
   that fails must not keep the previous user's driver either: today it is overwritten by `AuthUser`'s result).
   `PostAuthMessage` still gets the error as today.
2. `client_handler.go:459-497` (`readCommand`): stop the loop once the connection has been closed by
   `disconnect()`, whatever the idle timeout. E.g. a small `isClosed()` helper reading `connClosed` under
   `connMu.RLock`, checked before handling a line (right after `ReadLine`, so a line read from the buffer after the
   disconnect is dropped). `Close()` from another goroutine goes through `disconnect()` too, so it also stops the
   loop at the next line.
3. `driver.go`, `AuthUser` doc: say that the returned `ClientDriver` is ignored when an error is returned.

## Tests
In `handle_auth_test.go`:
- Regression (fails before the fix): a driver whose `AuthProvider` returns `(afero.NewMemMapFs(), err)`, settings
  `IdleTimeout: -1`. One write of `USER x`, `PASS wrong`, `MKD newdir`. Read the 530, wait for the server side to
  end (e.g. the client connection reads EOF), then check that `newdir` doesn't exist in the returned file system.
- Same with the default idle timeout: still no directory (it passes today, keeps the existing behaviour).
- Variant: a driver that returns `nil` on error, `IdleTimeout: -1`, one write of `USER x`, `PASS wrong`,
  `USER x`, `PASS good`, `MKD newdir`: the second `AuthUser` must not be called (count the calls), no directory.
- Positive control: with a good password, `USER`, `PASS`, `MKD newdir` in one write still creates the directory
  and gets 230 then 257, with `IdleTimeout: -1` and with the default.
- Unit test of `handlePASS`: after a failed `AuthUser` returning a non-nil driver, `c.driver` is nil.

## To verify
- No other caller relies on `c.driver` being set after a failed `PASS` (`end()`, `ClientDisconnected`, logging).
- `handleUserTLS` (`handle_auth.go:40-68`) already keeps the driver only on success: check it stays so.
