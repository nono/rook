# Rook: a dashboard of my work

## Intent

One web page that tells me, at a glance, what to do next:

- the PRs I must review,
- my PRs that need work from me,
- my PRs that wait for someone else,
- the other PRs of the team, for information.

Step 1 covers open GitHub PRs only, drafts included. Step 2 (later) adds Shortcut stories to the same page.

Success: I open the page in the morning and know my next action without a visit to GitHub.

## Goals

- Sort each open PR of the configured repos into exactly one group.
- Show the groups as rows, top to bottom, by priority.
- Show fresh data at each page load.
- Run on my machine with no new secret.
- Let me add a repo with one line of config.

## Non-goals (step 1)

- Shortcut stories (step 2; keep the data model open for them).
- Closed or merged PRs.
- Actions from the page (approve, merge, comment). The page only reads.
- Many users, a hosted deployment, login.
- Notifications.

## Anti-goals

- A PR that needs my review hidden in another group. A wrong group is worse than a slow page.
- A page that takes more than a few seconds to show.
- A new token to create and store.

## Constraints

- The user's GitHub login comes from the API (`viewer`), not from config.
- First repo: one work repo with about 35 open PRs today.
- The GitHub token comes from `gh auth token` at start-up.
- Shortcut workspace slug: from config (`scut whoami` gives it).
- `gh pr list --json` with reviews and checks fails with "exceeds the maximum limit of 500,000 nodes" and does not give the commit of each review. Use GraphQL directly.

## Approaches considered

1. **Local Go server (chosen).** `rook serve` listens on localhost. Each page load queries the GitHub GraphQL API and renders the page in Go. One binary, testable rules, room for Shortcut on the server side. Cost: a process to start.
2. **Static HTML generator.** `rook build` writes a file, a timer runs it again. Simpler, but the data is stale between runs and there is no refresh.
3. **Browser-only page** calling GitHub with a token in localStorage. Rejected: the token stays in the browser, and the Shortcut API probably blocks browser calls (CORS), which would break step 2.

## Grouping rules

Definitions:

- **Me**: the `viewer` login.
- **Human review**: a review whose author is a `User` (GraphQL `__typename`), not a `Bot`, and is not the PR author. Logins in the `ignore_reviewers` config list are also not human (for bots that show up as `User`).
- **My last review**: my most recent submitted review on the PR (`PENDING` drafts excluded). Any state counts: `COMMENTED`, `CHANGES_REQUESTED`, `APPROVED`, `DISMISSED`. A reply in a review thread is recorded by GitHub as a `COMMENTED` review on the head commit of that time, so it counts too.
- **My verdict**: the state of my most recent `APPROVED` or `CHANGES_REQUESTED` review. None if I have neither. A later `COMMENTED` review (such as a thread reply) does not change it.
- **Requested from me**: `reviewRequests` holds me as a `User`. A request to a team does not count (my call; see "Left to the builder").
- **CI state**: `statusCheckRollup.state` of the head commit. `SUCCESS` = green. `FAILURE` or `ERROR` = red. `PENDING` or `EXPECTED` = pending. No rollup (no checks) = green. GitHub already ignores runs replaced by a later run: a PR with a cancelled old run shows `SUCCESS`.
- **Conflict**: `mergeable == CONFLICTING`. `UNKNOWN` is not a conflict; the card shows "merge ?". GitHub computes it in the background; a later load resolves it.

Rules, in this order. The first match wins.

For a PR I did not author:

1. Requested from me → **review**. Label "not reviewed" if I have no review on it, else "requested again". A new request after my review means the author wants a second review, whatever my earlier verdict.
2. My last review exists, my verdict is not `APPROVED`, and the commit of my last review differs from the PR head commit → **review** ("new commits"). This rule does not need a pending request: GitHub removes the request when I submit a review.
3. Else → **team**.

Consequences, confirmed with the user:

- I approved, then replied in a thread, then the author pushed: **team** (my verdict is still `APPROVED`).
- I requested changes at commit A, the author pushed B, I replied in a thread at B: **team** (my last review is on the head commit). If the author wants a new review, they request it, and rule 1 applies. In most cases a second review is not needed.

For a PR I authored (drafts included; a draft follows the same rules):

4. CI red or pending → **WIP** ("CI red" / "CI pending").
5. No review requested from a human or a team, and no human review → **WIP** ("no reviewer").
6. Conflict → **conflict**.
7. Else → **waiting**.

A change request from a reviewer on my PR does not move it to WIP (confirmed with the user): we may wait for another reviewer, or for another PR to merge. The red avatar on the card shows it.

Display order: review, WIP, conflict, waiting, team. A group with no PR shows its header and "nothing".

## The page

Layout: rows. Each group is a band with a header (name, count with the group color, a short hint) and its cards, which wrap to the next line.

| Group | Color | Hint |
|---|---|---|
| review | orange `#ea580c` | your turn to review |
| WIP | amber `#ca8a04` | your turn to fix |
| conflict | red `#dc2626` | rebase needed |
| waiting | blue `#2563eb` | others' turn |
| team | gray `#6b7280` | FYI |

Card style: "Shortcut-like" (style C of the mockups, in `.superpowers/brainstorm/*/content/cards-v2.html`):

- a 4 px strip on top in the group color;
- the title, without its `[sc-NNNN]` prefix, bold;
- labels: the reason of the group ("CI red", "no reviewer", "conflict", "not reviewed", "new commits"), "draft", "merge ?";
- footer line 1: the story ID as a link to `https://app.shortcut.com/<workspace>/story/NNNN`, `#number`, a CI dot (green, red, amber, gray for no checks), and the reviewer avatars on the right;
- footer line 2: the author's avatar and login (or "mine"), and "updated Xd ago";
- the whole card opens the PR in a new tab; the story link opens the story and does not open the PR.

Reviewer avatars: one per human reviewer. The state is the reviewer's verdict (last `APPROVED` or `CHANGES_REQUESTED`), else "commented" if they have `COMMENTED` reviews, else "review dismissed" (faded, dotted ring) if they have only `DISMISSED` reviews. A pending request overrides all of these. Green ring and ✓ = approved, red ring and ✗ = changes requested, gray ring = commented, faded and dashed = review requested. The tooltip gives login and state.

Story ID: from the `[sc-NNNN]` prefix of the title. If there is none, from `sc-NNNN` in the head branch name. Else no link.

With more than one repo configured, the card shows the repo name.

Order inside a group: most recently updated first.

All cards show; the "+N more" of the mockups was only to keep the mockup short.

Top bar: the time of the data, a Refresh button (reloads the page), and an error banner per repo that failed. A failed repo does not hide the others.

## Architecture

- Go, one binary, `rook`. Templates and CSS embedded with `embed`. No JS framework; a few lines of JS at most.
- `rook serve` listens on a loopback address only: `127.0.0.1` by default; `localhost` and `[::1]` are also accepted.
- Config: TOML at `~/.config/rook/config.toml`, path overridable with `--config`.

  ```toml
  listen = "127.0.0.1:7878"
  repos = ["example-org/example-repo"]
  shortcut_workspace = "example-ws"
  ignore_reviewers = []
  ```

- Packages, split so the rules test without network:
  - **github**: one GraphQL query per repo, paginated (100 PRs per page), returns plain Go structs. Repos are fetched in parallel.
  - **board**: pure functions. Input: PRs and my login. Output: the groups with the cards and the reasons. Holds all the rules above.
  - **web**: the HTTP handler and the templates.
- Fields to fetch per PR: number, title, url, isDraft, updatedAt, headRefName, headRefOid, mergeable, author (login, avatarUrl, `__typename`), reviewRequests (User login/avatar, Team and EnterpriseTeam slug), reviews (id, state, submittedAt, commit oid, author login/avatar/`__typename`), also the last 100 reviews of the user (`author` filter; the id removes the reviews found twice), and the PR `statusCheckRollup.state` (GitHub: the rollup of the PR's head ref; it matched the rollup of the last commit on 134 open fork PRs of two public repos). Reviews can exceed one page on busy PRs (one PR of the work repo has about 90): fetch the last 100, which holds the last review of each person in practice.
- Each page load fetches fresh data. No cache.

## Tests

- **board**: table tests for each rule and each priority conflict: CI red and conflict → WIP; approved, then requested again → review ("requested again"); approved, thread reply, new push → team; changes requested at A, push B, thread reply at B → team; changes requested on my PR, green CI → waiting; draft of mine → normal rules; bot reviews ignored; author's own reviews ignored; `UNKNOWN` merge state.
- **github**: parse a recorded GraphQL response (fixture) into the structs; test pagination with a fake HTTP server.
- **web**: render the page from fixed groups; check the order of groups, the links, the labels.

## Step 2 (not now)

Shortcut stories as a second kind of card in the same groups. Keep the card type open (a kind field) so stories can join without a rewrite. The rules for stories are to decide later.

## Left to the builder

- HTTP port default, file layout, names of packages and types.
- How to start it at login (systemd user unit is a good fit). Optional.
- Exact GraphQL query text.
- Team review requests: treated as "not requested from me" for now. If the team asks for reviews through GitHub teams, this changes.
