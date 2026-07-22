# Sourcey documentation

This directory builds a Sourcey documentation site for ftpserverlib. It combines
five maintained guides with a generated Go API reference sourced from a committed
`godoc.json` snapshot.

The snapshot is pinned to commit
`b4c3694ee73399d8a55293d568e5100c25e4d2d4`, so documentation builds do not need
the Go toolchain and every generated source link resolves to the exact code that
was documented.

## Build and validate

```sh
npm ci
npm run build
npm run validate
npm run verify-receipt
```

`npm run verify-receipt` verifies the committed RunX receipt with its public
Ed25519 key. The private signing seed is not stored in this repository.

## Deployment

The `Sourcey docs` workflow builds pull requests and deploys the generated `dist`
directory to this repository's GitHub Pages site after a merge to `main`.
Repository owners need to select **GitHub Actions** as the Pages source once if it
is not already enabled.
