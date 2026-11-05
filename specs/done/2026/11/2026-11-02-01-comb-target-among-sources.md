---
model: opus
effort: low
pr: scan-specs-2026-11-02
automerge: false
---

# COMB that lists its target among its sources appends the file to itself without end

## Problem
Found by the security scan of 2026-11-02 (finding 2026-11-02-M1).
Severity: Medium. Who can trigger it: an authenticated user, when the server sets `EnableCOMB` (off by default,
`driver.go:369`).

`handleCOMB` (`handle_files.go:172-212`) turns the first parameter into the target path (`handle_files.go:187`)
and the others into source paths (`handle_files.go:191`), with no check that they differ. When the target exists,
it is opened with `os.O_APPEND`. `combineFiles` (`handle_files.go:214`) then opens each source for reading and
`io.Copy`s it into the target (`handle_files.go:233`).

When a source is the target itself (the same cleaned path, since both go through `absPath`), the copy reads the
file it is appending to: each read is followed by a write of the same size at the end, so the reader never reaches
EOF. The command never ends and the file grows until the disk (or the driver's quota, if it has one) is full. In a
local test on an SSD, one such command grew a 1 KB file to 931 MB in 3 seconds. It needs a single command on a
single connection and no data connection: it is not limited by the user's upload bandwidth. SECURITY.md lists
"exhaust its memory, file descriptors or ports, with a single connection or a few" as in scope, and filling the
storage is the same class.

A source listed twice has a smaller issue: it is removed after its first copy, so the second open fails and the
command stops halfway, with the first sources already deleted.

## Proposal
1. `handle_files.go` `handleCOMB`: after computing `targetPath` and `sourcePaths`, answer
   `StatusSyntaxErrorParameters` (501) and return, before any driver call, when a source path equals `targetPath`
   or when the same source path appears twice. Compare the cleaned absolute paths that `absPath` returns.
2. Keep the existing flow unchanged otherwise (existing target opened with `O_APPEND`, missing one created).

## Tests
- `handle_files_test.go`: with `EnableCOMB`, upload `a`, send `COMB a a` and `COMB a b a` (the target in a later
  position): each answers 501 at once, and the size of `a` is unchanged. Run the command with a timeout so the
  test fails (rather than hangs) before the fix.
- `handle_files_test.go`: `COMB t a ./a` (a source listed twice under two spellings) answers 501 and `a` still
  exists.
- Positive control: the existing `TestCOMB` and `TestCOMBAppend` still pass (distinct sources combined, partials
  removed, appending to an existing target still works).

## To verify
- Two different paths can still name the same file through a symlink or a hard link created by the driver
  (`SITE SYMLINK`). Detecting that is the driver's job (it can compare `os.SameFile` in its own `OpenFile`); say it
  in the `EnableCOMB` comment rather than resolving links in the library.
