---
name: ftpserverlib-sourcey-validation
description: Validate the pinned Sourcey build, generated pages, llms artifacts, and source mappings for ftpserverlib.
source:
  type: cli-tool
  command: node
  args:
    - run.mjs
  timeout_seconds: 30
  sandbox:
    profile: readonly
    cwd_policy: skill-directory
runx:
  artifacts:
    named_emits:
      validation: validation
---

Validate the reproducible Sourcey documentation build for the pinned
`fclairamb/ftpserverlib` commit and emit a structured summary suitable for a
governed receipt.
