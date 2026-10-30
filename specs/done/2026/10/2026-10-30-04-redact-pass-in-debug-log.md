---
model: opus
effort: low
pr: scan-specs-2026-10-30
automerge: false
---

# Debug logging writes the PASS password in clear

## Problem
Found by the security scan of 2026-10-30 (finding 2026-10-30-L2).
Severity: Low. Who can trigger it: any client, as soon as the driver enables debug for its connection
(`SetDebug`, often done for every client in `ClientConnected`).

`readCommand` logs every raw control line: `c.logger.Debug("Received line", "line", line)`
(`client_handler.go:487-491`). This includes `PASS <password>`, so user passwords end up in the server logs and
in whatever ships them (log aggregation, support bundles).

## Proposal
1. `client_handler.go` `readCommand`: before logging, redact the parameter of credential-carrying commands. Parse
   the line with `parseLine`; if the upper-cased command is `PASS`, log `PASS ****` (keep the line as is
   otherwise). A small helper (`redactLine`) keeps `readCommand` short.
2. Check whether any other debug log prints the PASS parameter (for example `executeCommandFn` or the panic
   recovery at `client_handler.go:648-658`, which logs `param`). Redact there too.

## Tests
- `client_handler_test.go`: a server with debug enabled and a logger writing to a buffer. Log in with a known
  password, and the buffer does not contain it but does contain the redacted `PASS` line. Fails before the fix.
- Positive control: other commands (`USER acme`, `CWD /dir`) are still logged verbatim.
