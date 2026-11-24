# Rook: show the PRs that have a Claude Code session

## Intent

When I work on one of my PRs with Claude Code, the card stays in Waiting. The work is in progress, so the card must be in WIP. Each card with an open Claude Code session also shows the Claude logo.

## Goals

- Find the open Claude Code sessions on this machine and the PR of each one.
- Move my PR with a session to WIP, label "Claude".
- Show the Claude logo on each card with a session, in all groups.
- No new config.

## Non-goals

- Idle or active sessions. An open session is work in progress, idle or not. A later change can make this difference (Claude Code hooks can record the state, on top of this design).
- Sessions on other machines, or other systems than Linux.
- PRs from a fork: a fork PR (`isCrossRepository`) never matches a session. Its head branch is in the fork, so a session on a branch of the same name in the base repo is not on that PR.

## Anti-goals

- A PR in Review that leaves Review because of a session. I review PRs with Claude Code.
- A page that is slower, or that fails, because of the sessions.

## Approaches considered

1. **Ask git in each cwd (chosen).** List the `claude` processes, read their cwd, and ask git for the branch and remote of that cwd. No config. A worktree created anywhere works. Cost: about 7 ms of git calls per session (45 ms for 6 sessions), run at the same time as the GitHub fetch.
2. **Worktree list of the configured clones.** A config key lists the local clones; `git worktree list --porcelain` gives the path and branch of each worktree, matched to the cwd by path. One git call per clone. Rejected: a new config key to keep current, and a clone not in the list is silently missed.
3. **Claude Code hooks.** SessionStart and SessionEnd hooks write a file per session. Rejected for now: hooks to set up, stale files after a crash, sessions started before the setup are missed. It stays the path to "idle or active" later.

## Sessions

A session is a process whose `/proc/PID/comm` is `claude` and whose `/proc/PID/cwd` Rook can read. In practice, those are the processes of the user that runs Rook.

The cwd is the directory where the session started. A `cd` in a Bash command of the session does not move it: Claude Code keeps the cwd of its shell in a file. So a session must start in the worktree of the PR.

For each cwd:

1. Branch: with no upstream, the remote is `origin` and the branch is the local branch name. With an upstream (`@{upstream}`, such as `origin/sc-1234/fix`), the remote is the upstream's remote, and the branch is:
   - the local name, when the upstream has the same name;
   - the local name, when the remote has a branch of that name (`refs/remotes/<remote>/<local>`): a branch made from `origin/main` and pushed without `-u` keeps the upstream `main`;
   - the local name, when the upstream is the remote's default branch (`refs/remotes/<remote>/HEAD`): a new branch made from `origin/main`, not pushed yet, is not on the PRs whose head is `main`;
   - else the upstream's name: a local name for a remote branch of another name.

   Branch names keep all their bytes. Branch names keep all their bytes. A rebase that stops on a conflict detaches `HEAD`: the local branch is then the branch under rebase (`rebase-merge/head-name` or `rebase-apply/head-name` in the git dir). Another detached `HEAD` gives no branch: no match.
2. Repo: the URL of that remote, as `owner/name`. Accept the scp-like form (`[user@]github.com:owner/name`) and the URLs (`ssh://`, `https://`), with a user, a port, a final `.git` or `/`, on the host `github.com` or `ssh.github.com`, in any case. Another host gives no match.
3. Match: a PR matches when the session's repo is equal to the configured name of the PR's repo or to its current name (`nameWithOwner`), and the session's branch is equal to the PR's head branch. GitHub follows a rename: a clone made before the rename has the old name in its remote URL, a new clone the new name. Repo names compare without case, branch names with case (git branch names depend on case).

A cwd that is not in a git repo, a process that ends during the scan (also during its git calls), or a git error: skip that process. If a rebase ends between the read of `HEAD` and the read of the rebase files, read `HEAD` again. git runs without the variables of `git rev-parse --local-env-vars` (such as `GIT_DIR`), which would make it read another repo. If `/proc` cannot be read (not Linux) or `git` is not in the PATH, there are no sessions: log the error once at start, show no banner. The sessions never make the page fail. The scan of one page load stops after 1 s, and git then has 100 ms (`WaitDelay`) to give back its output, so a hung git costs the page about 1.1 s at most. The lookups of the directories run in parallel, so a git that hangs in one directory does not stop the others.

Two sessions can match the same PR: the card keeps all the directories.

## Rules

The rules of the README and of `2026-11-24-dashboard-design.md` get one new rule for my PRs, between "no reviewer" and "conflict":

4. CI red or pending → WIP (no change).
5. No reviewer → WIP (no change).
6. **A Claude Code session is open on the PR → WIP ("Claude").**
7. Conflict → Conflict.
8. Waiting.

For a PR I did not author, rules 1 to 3 do not change: a session keeps Review in Review and Team in Team.

Consequences:

- Session and conflict → WIP ("Claude"): I am probably doing the rebase in that session.
- Session and CI red → WIP ("CI red"), with the logo.

## The page

The Claude logo goes in footer line 1, after the CI dot, on each card with a session, in all groups. Tooltip: "Claude Code in <dir>", one line per directory.

## Architecture

- New package (for example `internal/claude`) that gives the sessions: a list of (repo, branch, dir). It reads a `/proc` root given as a parameter, so tests use a temporary directory.
- `app.Load` reads the sessions at the same time as the PRs, and gives them to the board in `board.Options`.
- `board.Build` sets the session directories on the card and applies rule 6. The board stays pure.
- The logo is an inline SVG in `page.html`.

## Tests

- **board**: rule 6; Review with a session stays Review ("not reviewed" etc.); Team with a session stays Team, with the logo; CI red and session → "CI red"; conflict and session → WIP "Claude"; no session → no change; repo match without case; branch match with case.
- **sessions**: remote URL forms and other hosts; upstream branch with a name different from the local branch; no upstream; detached `HEAD`; a rebase stopped on a conflict; cwd not in a repo; a fake `/proc` with `claude` and other processes.
- **web**: the logo and its tooltip show when the card has a session, and not when it has none.
- README: the new rule and a line on the sessions (Linux only).

## Left to the builder

- Package, type and function names.
- How to call git (`git` commands or reading `.git` files). The `git` command is simpler and handles worktrees.
- The exact Claude logo SVG and its size.
