# Rook

Rook is a local web page that sorts the open pull requests of some GitHub repos into five groups. It tells you what to do next and what waits for someone else.

## Groups

Each PR goes into the first group that matches, in the order of this list. The page shows the groups from top to bottom: Review, WIP, Conflict, Waiting, then Team.

For a PR that you did not author:

1. **Review**: your review is requested from you, not from a team ("not reviewed" or "requested again").
2. **Review**: you reviewed it before, your last verdict is not an approval, and your last review is on a commit other than the head ("new commits").
3. **Team**: all the other PRs.

For your own PRs, drafts included:

4. **WIP**: the CI is red or pending.
5. **WIP**: no review is requested from a person or a team, and no person reviewed it ("no reviewer").
6. **WIP**: a Claude Code session is open on the PR's branch ("Claude").
7. **Conflict**: the PR has a git conflict.
8. **Waiting**: all the other PRs.

Your verdict is your last review that approves or requests changes. A reply in a review thread does not change it. Reviews by bots, by the PR author and by the logins in `ignore_reviewers` do not count.

A Claude Code session is a `claude` process whose directory is a git checkout of the PR's repo, on a local branch with the name of the PR's branch. The repo comes from the remote of the branch's upstream (else `origin`): a branch made from a branch of another remote matches only after `git push -u origin`. A `cd` in a Bash command of the session does not move its directory, so start the session in the worktree of the PR, or enter that worktree with the Claude Code worktree tool. An open session counts, idle or not. A session never moves a PR that you did not author, but each card with a session shows the Claude logo. This works on Linux only, with `git` in the PATH.

Each card of your PRs, drafts included, shows the CodeRabbit logo and a count when the PR has unresolved review threads that CodeRabbit started. A thread that you replied to counts until it is resolved. The logo does not change the group. Only the last 10 threads of a PR count: more threads make each page load cost more of the GitHub rate limit.

## Install

```sh
go install github.com/nono/rook@latest
```

Rook reads the GitHub token from `gh auth token`. Log in with `gh auth login` first.

## Config

Write `~/.config/rook/config.toml`:

```toml
# Required: the repos to show.
repos = ["example-org/example-repo"]

# Optional. Default "127.0.0.1:7878". Rook accepts only a loopback address.
listen = "127.0.0.1:7878"

# Optional: makes the sc-NNNN tags links to the Shortcut stories.
shortcut_workspace = "example-ws"

# Optional: logins that are not persons (bots that show as users).
ignore_reviewers = []
```

## Run

```sh
rook serve                  # or: rook serve --config PATH
```

Open <http://127.0.0.1:7878/>. Each page load reads fresh data from GitHub.

To start rook at login, write `~/.config/systemd/user/rook.service`:

```ini
[Unit]
Description=Rook dashboard

[Service]
ExecStart=%h/go/bin/rook serve
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
```

Rook runs `gh` at start, and `git` at each page load. If `gh` or `git` is not in the PATH that `systemctl --user show-environment` gives, add its directory to the unit, for example `Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin`.

If rook cannot start (network or `gh` not ready at login, or an error in the config), it stops and systemd starts it again 30 s later, with no end. For an error that does not go away, such as a bad config, read the cause with `journalctl --user -u rook`, fix it, and run `systemctl --user restart rook`.

Then run `systemctl --user enable --now rook`.

## Develop

```sh
go test ./...
golangci-lint run
```
