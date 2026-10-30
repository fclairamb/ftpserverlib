---
model: opus
effort: low
pr: scan-specs-2026-10-30
automerge: false
---

# HASH and the LIST option probe pass the raw client path to driver.Stat

## Problem
Found by the security scan of 2026-10-30 (finding 2026-10-30-L1).
Severity: Low. Who can trigger it: an authenticated user. The HASH part needs `EnableHASH`.

Every driver call receives a path from `absPath` (`handle_dirs.go:22-28`), which is absolute and cleaned. Two
calls do not:
- `handleGenericHash` calls `c.driver.Stat(args[0])` on the raw parameter (`handle_files.go:628`), then hashes
  `c.absPath(args[0])` (`handle_files.go:655-658`).
- `checkLISTArgs` calls `c.driver.Stat(args)` on the raw LIST parameter (`handle_dirs.go:174`).

A driver that relies on the documented contract and maps a path with something like
`filepath.Join(root, name)` without a prefix check gets a relative path whose `..` components leave its root.
The HASH reply then tells "is not a regular file" (`handle_files.go:635-638`) apart from an access error, and
echoes the driver error (`handle_files.go:629-633`): an existence/type oracle outside the user's tree. The size
used as the hash range (`handle_files.go:641`) can also come from another file than the one hashed. Relative
parameters are also resolved against the wrong directory (the driver's notion of the current directory instead
of `c.Path()`). `afero.BasePathFs` is not affected.

## Proposal
1. `handle_files.go` `handleGenericHash`: compute `filePath := c.absPath(args[0])` once and use it for `Stat`,
   `ComputeHash` and `computeHashForFile`.
2. `handle_dirs.go` `checkLISTArgs`: `Stat` `c.absPath(args)` instead of `args`.

## Tests
- `handle_files_test.go`: a test driver that records the paths given to `Stat`. `XSHA256 ../../x` (and a
  relative name after `CWD`) reaches `Stat` as the cleaned absolute path. Fails before the fix (raw string).
- `handle_dirs_test.go`: same recording for `LIST -la ../x`. `Stat` only sees absolute cleaned paths.
- Positive control: HASH on a relative file name in the current directory still returns the right hash, and
  `LIST -la` on an existing directory still lists it.
