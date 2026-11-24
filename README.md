# Rook

Rook is a local web page that sorts the open pull requests of some GitHub repos into five groups. It tells you what to do next and what waits for someone else.

## Groups

The groups show from top to bottom in this order. Each PR goes into the first group that matches.

For a PR that you did not author:

1. **Review**: your review is requested ("not reviewed" or "requested again").
2. **Review**: you reviewed it before, your last verdict is not an approval, and your last review is on an older commit than the head ("new commits").
3. **Team**: all the other PRs.

For your own PRs, drafts included:

4. **WIP**: the CI is red or pending.
5. **WIP**: no review is requested and no person reviewed it ("no reviewer").
6. **Conflict**: the PR has a git conflict.
7. **Waiting**: all the other PRs.

Your verdict is your last review that approves or requests changes. A reply in a review thread does not change it. Reviews by bots, by the PR author and by the logins in `ignore_reviewers` do not count.

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
StartLimitIntervalSec=0

[Service]
ExecStart=%h/go/bin/rook serve
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
```

Rook runs `gh` at start. If `gh` is not in `/usr/bin` or `/bin`, add its directory to the unit, for example `Environment=PATH=%h/.local/bin:/usr/bin:/bin`. If the network or `gh` is not ready at login, rook stops and systemd starts it again 30 s later, with no limit on the number of tries.

Then run `systemctl --user enable --now rook`.

## Develop

```sh
go test ./...
golangci-lint run
```
