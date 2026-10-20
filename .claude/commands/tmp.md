---
description: "Run a request with all temporary output in the project tmp/ directory"
argument-hint: "<request>"
---

Do the request below. For every temporary file and every piece of shell output that needs a temp location, use the `tmp/` directory in the project root.

Rules:
- Use `tmp/` in the project root (`$(git rev-parse --show-toplevel)/tmp/`). Create it if it does not exist.
- Do not write to `/tmp`, `/private/tmp`, `$TMPDIR`, `/var/folders`, or the session scratchpad directory.
- Redirect shell output that you save (logs, captures, intermediate results) into `tmp/`.
- Write scripts, scratch files, and generated reports into `tmp/`, unless the request names a different location.
- If a tool or command has a temp-dir flag or env var (for example `TMPDIR`, `--tmpdir`, `-o`), point it at `tmp/`.
- When you name a file in `tmp/` to the user, give its path relative to the project root.

Request:

$ARGUMENTS
