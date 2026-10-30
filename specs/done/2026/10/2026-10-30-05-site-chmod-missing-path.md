---
model: opus
effort: low
pr: scan-specs-2026-10-30
automerge: false
---

# SITE CHMOD without a path panics and echoes the runtime error

## Problem
Found by the security scan of 2026-10-30 (finding 2026-10-30-L3).
Severity: Low. Who can trigger it: an authenticated user.

`handleCHMOD` (`handle_files.go:263-279`) reads `spl[1]` without checking the length of
`strings.SplitN(params, " ", 2)`. `SITE CHMOD 644` panics with an index-out-of-range error.
`executeCommandFn` recovers it (`client_handler.go:648-658`) and sends `Unhandled internal error: runtime error:
index out of range ...` to the client. It does not crash, but it breaks the "no panic" rule and discloses an
internal error. `handleCHOWN` (`handle_files.go:285-290`) and `handleSYMLINK` (`handle_files.go:328-333`) do check
the length.

## Proposal
1. `handle_files.go` `handleCHMOD`: when `len(spl) != 2`, answer `StatusSyntaxErrorParameters` ("bad command"),
   as `handleCHOWN` does, and return before parsing the mode.

## Tests
- `handle_files_test.go`: `SITE CHMOD 644` answers 501 and the reply contains no "internal error". Fails before
  the fix.
- Positive control: `SITE CHMOD 600 file` still changes the mode and answers 200.
