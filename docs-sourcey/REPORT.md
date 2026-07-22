# Sourcey Go documentation report

This contribution adds a reproducible Sourcey documentation site for
`fclairamb/ftpserverlib`, a maintained MIT-licensed Go library.

The generated reference is pinned to commit
`b4c3694ee73399d8a55293d568e5100c25e4d2d4`. The committed GoDoc snapshot
contains 92 public API concepts, and the generated API page contains 65 links
back to exact lines at that commit. Five authored guides cover setup, the driver
contract, server settings, and extension points. The build also emits `llms.txt`
and `llms-full.txt` for machine-readable discovery.

Validation is intentionally independent of a local Go installation:

```sh
npm ci
npm run build
npm run validate
npm run verify-receipt
```

The governed RunX receipt uses `runx-cli 0.7.0` and a production Ed25519
signature. Its public verification key is committed in the verifier; the private
signing seed is not published.

The workflow validates every relevant pull request. After merge, it deploys the
same generated output to the repository-owned GitHub Pages site. If Pages has not
been enabled for this repository before, an owner needs to select **GitHub
Actions** as the Pages source once.
