# Security policy

## Reporting a vulnerability

Please **do not report security issues in public issues, pull requests or discussions**: they are visible to
everyone before a fix is out.

Report them privately instead, in one of these two ways:

1. **GitHub private vulnerability reporting (preferred):** open
   [a new security advisory](https://github.com/fclairamb/ftpserverlib/security/advisories/new)
   (the "Report a vulnerability" button in the repository's **Security** tab). Only the maintainers see it, and
   we can discuss the fix, credit you and publish the advisory from there.
2. **Email:** write to florent@clairambault.fr with `ftpserverlib security` in the subject.

Please include:

- the affected version (or commit) of ftpserverlib, and of the server built on it if relevant;
- what an attacker can do (read or write files outside the root, bypass authentication, crash the server…);
- how to reproduce it: the FTP commands sent, the settings and the driver used, a proof of concept if you have one;
- a fix suggestion, if you have one.

You will get an answer as soon as possible, usually within a few days. Once the fix is released, the advisory is
published with credit to you, unless you prefer to stay anonymous.

## Supported versions

Fixes go into the latest release. Please check that the issue still happens with it before reporting.

## Other private requests

For anything else that shouldn't be public (a question about a deployment, a commercial request), write to
florent@clairambault.fr.
