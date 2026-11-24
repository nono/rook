# rook

- Run `make check` before each commit. `mise.toml` pins the tools.
- The `## Groups` section of the README is the spec of `internal/board` and `internal/claude`. Change it in the same commit as the rule. A file in `docs/design/` is a dated record: it freezes at the commit that adds it, and later changes go to the README. A difference between a design doc and the code is not a finding.
- rook is a single-user tool. Document a rare limit instead of adding code for it. A missing test of a one-line call site, or of a rare git setup, is a low finding.
