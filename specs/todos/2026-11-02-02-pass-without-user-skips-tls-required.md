---
model: opus
effort: low
pr: scan-specs-2026-11-02
automerge: false
---

# PASS without a USER logs in without the TLSRequired check

## Problem
Found by the security scan of 2026-11-02 (finding 2026-11-02-L1).
Severity: Low. Who can trigger it: an unauthenticated client, against a driver whose `AuthUser` accepts an empty
username.

`TLSRequired = MandatoryEncryption` (and the per-client `SetTLSRequirement(MandatoryEncryption)`, whose contract
says it "is checked when the client issues the USER command", `driver.go:197-200`) is only enforced in
`handleUSER` (`handle_auth.go:20-25`). `PASS` is open before login (`server.go:38`) and `handlePASS`
(`handle_auth.go:72-101`) calls `AuthUser(c, c.user, param)` (`handle_auth.go:75`) with no TLS check and no
check that a `USER` came first, so `c.user` is `""`.

A client that sends `PASS <secret>` as its first command on a clear-text connection therefore skips both the TLS
check and `MainDriverExtensionUserVerifier.PreAuthUser`. When the driver accepts an empty username (password-only
or token logins, for example: acme's driver maps an API token to an account), the client is logged in over clear
text and every later control command runs in clear text, which `MandatoryEncryption` is meant to prevent. A local
test with a token-only `AuthProvider` and `MandatoryEncryption` got `230` for `PASS token`, then `257` for `PWD`.
SECURITY.md lists "bypass `TLSRequired`" as in scope. With a driver that refuses an empty username, `AuthUser`
fails and the client is disconnected, so the impact depends on the driver.

## Proposal
1. `handle_auth.go` `handlePASS`: before calling `AuthUser`, when `c.isTLSRequired() && !c.HasTLSForControl()`,
   answer `StatusServiceNotAvailable` ("TLS is required") and disconnect, as `handleUSER` does.
2. `handle_auth.go` `handlePASS`: when no `USER` was accepted on this connection, answer
   `StatusBadCommandSequence` (503, "Login with USER first", RFC 959) and do not call `AuthUser`. Track it with a
   flag set by `handleUSER` once it accepts the user (rather than testing `c.user != ""`, since `USER` with an
   empty parameter is accepted today), and reset it after each `PASS`.
3. Update the `SetTLSRequirement` comment (`driver.go:197-200`) to say the requirement is checked on `USER` and
   `PASS`.

## Tests
- `handle_auth_test.go`: with `MandatoryEncryption` and a test `AuthProvider` that accepts any user with password
  `token`, a clear-text `PASS token` sent first gets no `230`, and a following `PWD` gets no `257`. Fails before the
  fix.
- `handle_auth_test.go`: with TLS not required, `PASS` sent before any `USER` answers 503 and `AuthUser` is not
  called.
- Positive control: the existing `TestAuthTLSRequired` still passes (explicit TLS then `USER`/`PASS` logs in), and
  a plain `USER`/`PASS` login without TLS required still works.

## Open questions
- Should step 2 (503 for `PASS` before `USER`) ship, or only the TLS check of step 1? Recommended: ship both. Step
  1 closes the `TLSRequired` bypass, and step 2 also stops `PreAuthUser` from being skipped. No mainstream client
  sends `PASS` first, and RFC 959 expects 503 there.

## Resolved open questions
- Ship both step 1 (TLS check) and step 2 (503 for `PASS` before `USER`). (autopilot: recommended answer)
