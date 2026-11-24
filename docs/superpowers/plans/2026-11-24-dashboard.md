# Rook dashboard (step 1: GitHub PRs) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A local web page that sorts the open PRs of the configured repos into the groups review, WIP, conflict, waiting, team.

**Architecture:** One Go binary, `rook serve`. Each page load runs one paginated GraphQL query per repo (in parallel), passes the PRs to pure grouping functions, and renders an embedded `html/template` page. Config in TOML. Token from `gh auth token`.

**Tech Stack:** Go 1.27, standard library (`net/http`, `html/template`, `embed`, `os/exec`), `github.com/BurntSushi/toml` v1.6.0.

**Spec:** `docs/design/2026-11-24-dashboard-design.md`

## Global Constraints

- Module path: `github.com/nono/rook`. Go directive: `go 1.27`.
- One dependency outside the standard library: `github.com/BurntSushi/toml`.
- Listen on a loopback address only. Default `127.0.0.1:7878`.
- Config default path: `os.UserConfigDir()` + `/rook/config.toml` (so `~/.config/rook/config.toml`); `--config` overrides.
- My login comes from GraphQL `viewer { login }`, read once at start-up.
- GitHub GraphQL endpoint: `https://api.github.com/graphql`, header `Authorization: bearer <token>`.
- Each page load fetches fresh data. No cache.
- Group order and copy, exact:

  | Group const | Name | Hint | Color |
  |---|---|---|---|
  | `review` | Review | your turn to review | `#ea580c` |
  | `wip` | WIP | your turn to fix | `#ca8a04` |
  | `conflict` | Conflict | rebase needed | `#dc2626` |
  | `waiting` | Waiting | others' turn | `#2563eb` |
  | `team` | Team | FYI | `#6b7280` |

- Reason labels, exact: `not reviewed`, `requested again`, `new commits`, `CI red`, `CI pending`, `no reviewer`, `conflict`. Extra labels: `draft`, `merge ?`.
- Shortcut story URL: `https://app.shortcut.com/<workspace>/story/<id>`.
- After each task: `gofmt -l .` prints nothing, `go vet ./...` and `golangci-lint run` pass.
- Commit messages: a short description that starts with a capital letter, no final period, no `feat:` prefix. End with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. A repo fails (typo in its name, no access, rate limit): the page shows a banner for that repo and the cards of the other repos. Test: `TestLoadKeepsOtherReposWhenOneFails` (Task 5).
2. A PR title that holds HTML (`<b>`, `<script>`): the page shows it as text. Test: `TestPageEscapesTitle` (Task 4).
3. A repo with more than 100 open PRs: all PRs show. Test: `TestOpenPRsFollowsPages` (Task 3).
4. A PR or review by a deleted account (GraphQL `author: null`): shows as `ghost`, no panic. Test: `TestOpenPRsNullAuthor` (Task 3).
5. A typo in the config (`repo = …` instead of `repos`) or a non-loopback `listen`: `rook` stops at start with a message that names the problem. Tests: `TestLoadRejectsUnknownKey`, `TestLoadRejectsNonLoopback` (Task 1).

---

## File Structure

```
go.mod, go.sum
main.go                          flags, wiring, gh token, HTTP server
internal/config/config.go        Config, Load, DefaultPath
internal/config/config_test.go
internal/github/types.go         PR, Actor, Review (plain data)
internal/github/client.go        Client, Viewer, OpenPRs (GraphQL + pagination)
internal/github/query.graphql    the PR query (embedded)
internal/github/client_test.go
internal/github/testdata/*.json  hand-written GraphQL responses
internal/board/board.go          groups, Classify, Reviewers, StoryID, Build
internal/board/board_test.go
internal/web/handler.go          Handler, Age
internal/web/page.html           template + inline CSS (style C)
internal/web/handler_test.go
internal/app/app.go              Load: fetch all repos in parallel, build the board
internal/app/app_test.go
```

---

### Task 1: Module and config

**Files:**
- Create: `go.mod`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  ```go
  package config
  type Config struct {
      Listen            string   `toml:"listen"`
      Repos             []string `toml:"repos"`
      ShortcutWorkspace string   `toml:"shortcut_workspace"`
      IgnoreReviewers   []string `toml:"ignore_reviewers"`
  }
  func Load(path string) (Config, error)
  func DefaultPath() (string, error)
  ```

- [ ] **Step 1: `go mod init github.com/nono/rook`, set `go 1.27`, `go get github.com/BurntSushi/toml@v1.6.0`.**

- [ ] **Step 2: Write the failing tests** (each writes a file in `t.TempDir()`):
  - `TestLoadFull`: all four keys set → fields equal the file values.
  - `TestLoadDefaultListen`: only `repos = ["a/b"]` → `Listen == "127.0.0.1:7878"`.
  - `TestLoadRejectsUnknownKey`: `repo = ["a/b"]` → error whose text contains `repo`.
  - `TestLoadRejectsNoRepos`: empty file → error contains `repos`.
  - `TestLoadRejectsBadRepo`: each of `"a"`, `"a/"`, `"/b"`, `"a/b/c"` → error contains the bad value.
  - `TestLoadRejectsNonLoopback`: `listen = "0.0.0.0:7878"` → error contains `loopback`. `localhost:1`, `127.0.0.1:1`, `[::1]:1` are accepted.
  - `TestLoadMissingFile`: missing path → error contains the path.

- [ ] **Step 3: Run `go test ./internal/config/`.** Expected: FAIL (undefined `Load`).

- [ ] **Step 4: Implement `Load` and `DefaultPath`.** Use `toml.DecodeFile` and `MetaData.Undecoded()` for unknown keys. Loopback check: `net.SplitHostPort`, then host is `localhost` or `net.ParseIP(host).IsLoopback()`.

- [ ] **Step 5: Run `go test ./internal/config/`.** Expected: PASS.

- [ ] **Step 6: Commit** `go.mod go.sum internal/config/` with message `Add the config of rook`.

---

### Task 2: Grouping rules (board)

**Files:**
- Create: `internal/github/types.go`, `internal/board/board.go`, `internal/board/board_test.go`

**Interfaces:**
- Produces (`internal/github/types.go`, data only):
  ```go
  package github
  type Actor struct { Login, AvatarURL string; Bot bool }
  type Review struct {
      Author      Actor
      State       string    // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED (PENDING removed by the client)
      SubmittedAt time.Time
      CommitOID   string
  }
  type PR struct {
      Repo           string // "owner/name"
      Number         int
      Title, URL     string
      IsDraft        bool
      UpdatedAt      time.Time
      HeadRefName    string
      HeadOID        string
      Mergeable      string // MERGEABLE | CONFLICTING | UNKNOWN
      Author         Actor
      RequestedUsers []Actor
      RequestedTeams []string // team slugs
      Reviews        []Review // any order
      CIState        string   // statusCheckRollup.state; "" when no checks
  }
  ```
- Produces (`internal/board/board.go`):
  ```go
  package board
  type Group string
  const (Review Group = "review"; WIP = "wip"; Conflict = "conflict"; Waiting = "waiting"; Team = "team")
  var Order = []Group{Review, WIP, Conflict, Waiting, Team}
  type CI string
  const (CIGreen CI = "green"; CIRed = "red"; CIPending = "pending"; CINone = "none")
  type ReviewerState string
  const (Approved ReviewerState = "approved"; Changes = "changes"; Commented = "commented"; Requested = "requested")
  type Reviewer struct { Login, AvatarURL string; Team bool; State ReviewerState }
  type Label struct { Text, Tone string } // Tone: "red" | "ylw" | "gry"
  type Card struct {
      Kind      string // "pr"; step 2 adds "story"
      Repo      string
      ShowRepo  bool
      Number    int
      Title     string // without the [sc-NNNN] prefix
      URL       string
      StoryID   string // "" when none
      StoryURL  string // "" when no ID or no workspace
      Group     Group
      Labels    []Label
      CI        CI
      Author    github.Actor
      Mine      bool
      Reviewers []Reviewer
      UpdatedAt time.Time
  }
  type Section struct { Group Group; Name, Hint, Color string; Cards []Card }
  type RepoError struct { Repo, Err string }
  type Board struct { Sections []Section; Errors []RepoError; FetchedAt time.Time }
  type Options struct { Me, ShortcutWorkspace string; Ignore []string; ShowRepo bool }
  func CIOf(state string) CI
  func Classify(pr github.PR, opts Options) (Group, string) // group, reason label ("" for team and waiting)
  func Reviewers(pr github.PR, opts Options) []Reviewer
  func StoryID(title, branch string) (id, cleanTitle string)
  func Build(prs []github.PR, opts Options) []Section // always 5 sections, in Order
  ```

Rules to implement, from the spec (first match wins):

- Human review: author not `Bot`, login not in `Ignore`, login not the PR author.
- My reviews: reviews whose author login is `Me`. My last review: latest by `SubmittedAt`. My verdict: state of my latest `APPROVED` or `CHANGES_REQUESTED` review.
- Not mine: (1) `Me` in `RequestedUsers` → `review`, `not reviewed` if I have no review, else `requested again`. (2) my last review exists, verdict ≠ `APPROVED`, last review `CommitOID` ≠ `HeadOID` → `review`, `new commits`. (3) else `team`.
- Mine: (4) CI red/pending → `wip`, `CI red`/`CI pending`. (5) no `RequestedUsers`, no `RequestedTeams`, no human review → `wip`, `no reviewer`. (6) `CONFLICTING` → `conflict`, `conflict`. (7) else `waiting`.
- `CIOf`: `SUCCESS`→green; `FAILURE`,`ERROR`→red; `PENDING`,`EXPECTED`→pending; `""`→none. `none` counts as green for rule 4.
- `Reviewers`: one entry per human reviewer, in order of first review; state = verdict, else `commented`; a user in `RequestedUsers` gets `requested` (added if they have no review). Each team in `RequestedTeams` → `Team: true`, `State: requested`. Bots and ignored logins never appear.
- `StoryID`: title prefix `^\s*\[sc-(\d+)\]\s*` (removed from the title); else branch matches `(?i)(?:^|[^a-z0-9])sc-(\d+)`; else `""`.
- Labels on a card, in order: reason (tone `red` for `CI red`, `conflict`; `ylw` for `CI pending`, `no reviewer`; `gry` for the others), then `draft` (gry) if draft, then `merge ?` (gry) if `UNKNOWN`.
- `Build`: `Kind = "pr"` on each card; cards sorted by `UpdatedAt` desc, then `Number` desc; empty sections kept with `Cards == nil`.

- [ ] **Step 1: Write the failing table test `TestClassify`.** Helper `pr(mods ...func(*github.PR))` builds a PR by someone else with head `h2`. `Me = "me"`. Cases (name → group, reason):
  - `requested, never reviewed` → `review`, `not reviewed`
  - `approved, requested again` → `review`, `requested again`
  - `commented at h1, head h2` → `review`, `new commits`
  - `changes at h1, head h2` → `review`, `new commits`
  - `approved at h1, head h2` → `team`, `""`
  - `approved h1, reply (COMMENTED) h1, head h2` → `team`
  - `changes at h1, reply (COMMENTED) at h2, head h2` → `team`
  - `dismissed at h1, head h2` → `review`, `new commits` (no verdict, last review on old commit)
  - `not requested, never reviewed` → `team`
  - `draft by someone else, requested` → `review`, `not reviewed`
  - mine: `CI FAILURE + CONFLICTING` → `wip`, `CI red`
  - mine: `CI ERROR` → `wip`, `CI red`; `CI PENDING` → `wip`, `CI pending`; `CI EXPECTED` → `wip`, `CI pending`
  - mine: `CI "" , requested user` → `waiting`
  - mine: `no requests, only bot review` → `wip`, `no reviewer`
  - mine: `no requests, only my own review` → `wip`, `no reviewer`
  - mine: `no requests, review by an ignored login` (`Ignore: ["rabbit"]`, `Bot: false`) → `wip`, `no reviewer`
  - mine: `team requested only` → `waiting`
  - mine: `human review, no request, CONFLICTING` → `conflict`, `conflict`
  - mine: `changes requested by human, CI green` → `waiting`
  - mine: `draft, CI green, requested` → `waiting`
  - mine: `UNKNOWN mergeable, requested, CI green` → `waiting`

- [ ] **Step 2: Write `TestReviewers`**: reviews `alice APPROVED`, `bob CHANGES_REQUESTED`, `carol COMMENTED`, `alice COMMENTED` (later), `rabbit[bot] COMMENTED` (`Bot: true`), author `COMMENTED`; requested `bob`, `dave`; team `core` → `[{alice approved} {bob requested} {carol commented} {dave requested} {core Team requested}]`.

- [ ] **Step 3: Write `TestStoryID`**: `("[sc-1234] Fix the parser", "x")` → `"1234", "Fix the parser"`; `("Add a flag", "alice/sc-12-flag")` → `"12", "Add a flag"`; `("Fix disc-5", "misc-5")` → `"", "Fix disc-5"`; `("Fix", "sc-7")` → `"7"`.

- [ ] **Step 4: Write `TestBuild`**: 3 PRs (one review, two team with different `UpdatedAt`) and `ShortcutWorkspace: "ws"` → 5 sections in `Order` with the exact names, hints and colors of Global Constraints; team cards newest first; card with `[sc-9]` has `StoryURL == "https://app.shortcut.com/ws/story/9"`; with `ShortcutWorkspace: ""` → `StoryID == "9"`, `StoryURL == ""`; a draft `UNKNOWN` PR of mine with red CI has labels `[{CI red red} {draft gry} {merge ? gry}]`; `ShowRepo` copies to the cards; `Mine` true only for my PRs.

- [ ] **Step 5: Run `go test ./internal/board/`.** Expected: FAIL (undefined symbols).

- [ ] **Step 6: Implement `types.go` and `board.go`** with the signatures and rules above.

- [ ] **Step 7: Run `go test ./internal/board/`.** Expected: PASS.

- [ ] **Step 8: Commit** `internal/github/types.go internal/board/` with message `Sort the PRs into the groups of the dashboard`.

---

### Task 3: GitHub GraphQL client

**Files:**
- Create: `internal/github/client.go`, `internal/github/query.graphql`, `internal/github/client_test.go`, `internal/github/testdata/viewer.json`, `internal/github/testdata/page1.json`, `internal/github/testdata/page2.json`

**Interfaces:**
- Consumes: `github.PR`, `github.Actor`, `github.Review` (Task 2).
- Produces:
  ```go
  const DefaultEndpoint = "https://api.github.com/graphql"
  type Client struct { Endpoint, Token string; HTTP *http.Client }
  func (c *Client) Viewer(ctx context.Context) (string, error)
  func (c *Client) OpenPRs(ctx context.Context, repo string) ([]PR, error) // repo is "owner/name"
  ```

The query (fields checked against the live API on 2026-11-24): `repository(owner:$owner, name:$name) { pullRequests(states: OPEN, first: 100, after: $cursor) { pageInfo { hasNextPage endCursor } nodes { number title url isDraft updatedAt headRefName headRefOid mergeable author { __typename login avatarUrl } reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login avatarUrl } ... on Team { slug } } } } reviews(last: 100) { nodes { state submittedAt commit { oid } author { __typename login avatarUrl } } } commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } } } }`.

Mapping: `__typename == "Bot"` → `Actor.Bot`. `author: null` → `Actor{Login: "ghost"}`. Drop reviews with state `PENDING`. `commit: null` on a review → `CommitOID ""`. No rollup → `CIState ""`. Set `PR.Repo`.

- [ ] **Step 1: Write the testdata files** by hand: `viewer.json` (`{"data":{"viewer":{"login":"me"}}}`); `page1.json` with 2 PRs and `hasNextPage: true, endCursor: "c1"`; `page2.json` with 1 PR and `hasNextPage: false`. Include: a Bot reviewer, a `PENDING` review, a team request, a PR with `author: null`, a PR with `statusCheckRollup: null`.

- [ ] **Step 2: Write the failing tests** with `httptest.NewServer`:
  - `TestViewer`: returns `"me"`; the request has header `Authorization: bearer tok` and method `POST`.
  - `TestOpenPRsFollowsPages`: server answers page1 when `variables.cursor` is null, page2 when it is `"c1"` → 3 PRs; `owner`/`name` variables split from `"o/r"`; exactly 2 requests.
  - `TestOpenPRsMapsFields`: on the parsed PRs: Bot flag set, `PENDING` review dropped, team slug in `RequestedTeams`, `CIState ""` for null rollup, `Repo == "o/r"`, review `CommitOID` and `SubmittedAt` parsed.
  - `TestOpenPRsNullAuthor`: the PR with `author: null` has `Author.Login == "ghost"`.
  - `TestOpenPRsGraphQLError`: body `{"errors":[{"message":"Could not resolve to a Repository"}]}` → error contains that message and the repo name.
  - `TestOpenPRsHTTPError`: status 401 → error contains `401`.
  - `TestOpenPRsBadRepo`: `"norepo"` → error, no request sent.

- [ ] **Step 3: Run `go test ./internal/github/`.** Expected: FAIL.

- [ ] **Step 4: Implement `client.go`.** Embed `query.graphql` with `//go:embed`. One `post(ctx, query, vars, out)` helper that sends `{"query","variables"}`, checks the status code, decodes `{"data","errors"}`, and returns the first error message. Wrap errors with the repo name.

- [ ] **Step 5: Run `go test ./internal/github/`.** Expected: PASS.

- [ ] **Step 6: Check against the live API:** write a throwaway `go run` program in the scratchpad (not committed) that calls `OpenPRs` on `example-org/example-repo` with `gh auth token` → it prints about 35 PRs and the time taken. Note the time in the commit body.

- [ ] **Step 7: Commit** `internal/github/` with message `Read the open PRs of a repo from the GitHub GraphQL API`.

---

### Task 4: Web page

**Files:**
- Create: `internal/web/handler.go`, `internal/web/page.html`, `internal/web/handler_test.go`

**Interfaces:**
- Consumes: `board.Board`, `board.Section`, `board.Card`, `board.Reviewer`, `board.Label`, `board.CI` (Task 2).
- Produces:
  ```go
  package web
  type Source func(ctx context.Context) board.Board
  func NewHandler(src Source, now func() time.Time) http.Handler
  func Age(d time.Duration) string // "5m", "3h", "2d"; under a minute → "0m"
  ```

Page content follows the spec section "The page" and style C. Reference markup and CSS: option C of `.superpowers/brainstorm/2135708-1795510663/content/cards-v2.html` (the `.cc`, `.hc`, `.lab`, `.scid`, `.avw`, `.mk`, `.ci` rules and the shared rules above them). Reuse that CSS; replace the inline `onclick` handlers as below. Specifics:
- Each section: `<section id="g-<group>">`, header with name, count badge in the group color, hint. Empty → `<div class="empty">nothing</div>`.
- Card: `<div class="card" data-url="…">` with the 4 px strip in the group color; the whole card opens `URL` in a new tab through a small inline script that reads `data-url` (no inline `onclick` with the URL in it); the story link is `<a class="scid" href="StoryURL" target="_blank">sc-ID</a>` and its click does not open the PR (`event.stopPropagation()`); without `StoryURL` but with `StoryID`, plain text `sc-ID`.
- Footer 1: story, `#Number`, CI dot (`●` green/red, `◐` amber, `○` gray, `title` = `CI <state>`), reviewer avatars right (`<img src=AvatarURL>` with ring class = state, ✓ for approved, ✗ for changes, `title="login: state word"`; a team shows a gray pill with its slug).
- Footer 2: author avatar and login, or `mine`; `updated <Age> ago`. With `ShowRepo`, the repo name before the title.
- Top bar: `Data from <FetchedAt local time HH:MM:SS>`, a `Refresh` button (`location.reload()`), one red banner per `RepoError`: `<repo>: <err>`.
- Only `GET /` serves the page. Other paths → 404. Other methods → 405.

- [ ] **Step 1: Write the failing tests:**
  - `TestAge`: `30s`→`0m`, `5m`→`5m`, `3h10m`→`3h`, `49h`→`2d`.
  - `TestPageOrderAndContent`: a board with one card in review (story `9`, URL `https://github.com/o/r/pull/1`), waiting empty, an error `{o/x, boom}` → status 200; `g-review` < `g-wip` < `g-conflict` < `g-waiting` < `g-team` by index in the body; body contains `https://app.shortcut.com/ws/story/9`, `data-url="https://github.com/o/r/pull/1"`, `nothing`, `o/x: boom`, `Refresh`, `your turn to review`, the label texts of the card, `stopPropagation`.
  - `TestPageEscapesTitle`: title `<script>alert(1)</script>` → body does not contain `<script>alert(1)`, contains `&lt;script&gt;`.
  - `TestNotFound`: `GET /x` → 404. `TestMethodNotAllowed`: `POST /` → 405.

- [ ] **Step 2: Run `go test ./internal/web/`.** Expected: FAIL.

- [ ] **Step 3: Implement `handler.go` and `page.html`** (template parsed once at `NewHandler`, embedded with `//go:embed page.html`; the handler calls `src(r.Context())` on each request).

- [ ] **Step 4: Run `go test ./internal/web/`.** Expected: PASS.

- [ ] **Step 5: Commit** `internal/web/` with message `Render the dashboard page`.

---

### Task 5: App wiring and `rook serve`

**Files:**
- Create: `internal/app/app.go`, `internal/app/app_test.go`, `main.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Load`, `config.DefaultPath` (Task 1); `board.Build`, `board.Options`, `board.Board`, `board.RepoError` (Task 2); `github.Client`, `github.PR` (Task 3); `web.NewHandler`, `web.Source` (Task 4).
- Produces:
  ```go
  package app
  type Fetcher interface { OpenPRs(ctx context.Context, repo string) ([]github.PR, error) }
  func Load(ctx context.Context, f Fetcher, me string, cfg config.Config, now func() time.Time) board.Board
  ```

`Load` fetches all repos in parallel (one goroutine per repo, `sync.WaitGroup`), keeps the order of `cfg.Repos` for errors, builds with `ShowRepo: len(cfg.Repos) > 1`, and sets `FetchedAt = now()`. Each page load gets a 20 s timeout on its context.

`main.go`: `rook serve [--config PATH]`. Unknown or missing subcommand → usage on stderr, exit 2. Steps at start: load config; token from `exec.Command("gh", "auth", "token")` (trimmed; empty or error → message `run "gh auth login" first`, exit 1); `Viewer` once (error → exit 1); `http.Server{Addr: cfg.Listen, ReadHeaderTimeout: 5 * time.Second}`; log `rook: http://<listen>/` on start; stop on SIGINT/SIGTERM with `Shutdown`.

- [ ] **Step 1: Write the failing tests** with a fake `Fetcher` (map repo → PRs or error):
  - `TestLoadKeepsOtherReposWhenOneFails`: repos `o/a` (1 PR), `o/b` (error `boom`) → 1 card in the board, `Errors == [{o/b, …boom…}]`.
  - `TestLoadShowRepo`: 2 repos → cards have `ShowRepo` true; 1 repo → false.
  - `TestLoadFetchedAt`: `FetchedAt` equals the fixed `now`.
  - `TestLoadParallel`: fake blocks each call until both have started (a `sync.WaitGroup` with a 1 s timeout) → no timeout.

- [ ] **Step 2: Run `go test ./internal/app/`.** Expected: FAIL.

- [ ] **Step 3: Implement `app.go` and `main.go`.**

- [ ] **Step 4: Run `go test ./...`.** Expected: PASS.

- [ ] **Step 5: Run the real thing:** write `~/.config/rook/config.toml` with `repos = ["example-org/example-repo"]`, `shortcut_workspace = "example-ws"` (ask the user before you write it, if it exists); `go run . serve`; `curl -s http://127.0.0.1:7878/ | grep -c 'class="card"'` → about 35; open the page in the browser and compare with the style C mockup. Also check: a config with `repos = ["example-org/nope", "example-org/example-repo"]` → banner for `nope`, cards for the other.

- [ ] **Step 6: Commit** `internal/app/ main.go` with message `Serve the dashboard with rook serve`.

---

### Task 6: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write `README.md`** (STE, short): what rook shows, the group rules in one list (copy from the spec), install (`go install github.com/nono/rook@latest` or `go build`), the config file with the four keys and defaults, `rook serve`, and an optional systemd user unit (`~/.config/systemd/user/rook.service`, `ExecStart=%h/go/bin/rook serve`, `systemctl --user enable --now rook`).

- [ ] **Step 2: Commit** `README.md` with message `Add the README`.

- [ ] **Step 3: Delete this plan** before the merge (user rule): `git rm docs/superpowers/plans/2026-11-24-dashboard.md`, commit `Remove the implementation plan`.
