# Security policy

## Reporting a vulnerability

**Never report a vulnerability in a public issue, pull request, discussion or commit message**: it would be visible
to everyone before a fix is out.

Report it privately instead:

1. **GitHub private vulnerability reporting (preferred):** open
   [a new security advisory](https://github.com/fclairamb/ftpserverlib/security/advisories/new)
   (the "Report a vulnerability" button in the repository's **Security** tab). Only the maintainers see it.
2. **Email:** write to florent@clairambault.fr with `ftpserverlib security` in the subject.

Please include:

- the affected version (or commit) of ftpserverlib, and of the server built on it if relevant;
- the impact: what an attacker can do, and whether they need to be logged in;
- how to reproduce it: the FTP commands sent, the `Settings` and the driver used, a proof of concept if you have one;
- a fix suggestion, if you have one.

Only test against servers you own or are allowed to test.

## What counts as a vulnerability

ftpserverlib implements the FTP protocol. Authentication, authorization and storage belong to the driver the
application provides (`MainDriver`, `ClientDriver`, its `afero.Fs`). A report is **in scope** when the library itself
lets a client do something the driver or the `Settings` did not allow, for example:

- run a command or reach a file before a successful login, or bypass `TLSRequired`;
- hijack or read another client's data connection (passive or active mode);
- send a path that reaches the driver unclean (not absolute, or with `..` left in it);
- crash the server, or exhaust its memory, file descriptors or ports, with a single connection or a few;
- leak credentials or another client's data (in replies or in the logs).

**Out of scope**, because it belongs to the application or is a documented choice:

- a driver that lets a user reach files outside their root: confining a user is the job of the driver's `afero.Fs`
  (e.g. `afero.NewBasePathFs`); report it to the application (for [ftpserver](https://github.com/fclairamb/ftpserver),
  in that repository);
- weak authentication, password storage or user management in a driver;
- clear-text FTP when `TLSRequired` is not set, and the data connection checks turned off with `IPMatchDisabled`;
- denial of service that needs many connections: limiting connections, rate and bandwidth is the application's or
  the network's job;
- vulnerabilities in dependencies that are already public: they are handled by the regular dependency updates.

When in doubt, report it privately anyway.

## How a report is handled

1. **Acknowledgement** within 7 days.
2. **Assessment**: we confirm the issue (or explain why it is out of scope) and agree on its severity.
3. **Fix** in the advisory's private fork, never in a public pull request, aiming at 90 days at most, sooner for a
   severe issue. You are welcome to review it.
4. **Release and disclosure**: the fix is released, then the advisory is published with a CVE and credit to you
   (unless you prefer to stay anonymous). Please keep the issue private until then.

## Supported versions

Fixes go into the next release of the latest version only; older versions don't get backports. Please check that the
issue still happens with the latest release before reporting.

## Other private requests

For anything else that shouldn't be public (a question about a deployment, a commercial request), write to
florent@clairambault.fr.
