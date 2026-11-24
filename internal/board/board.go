// Package board sorts pull requests into the groups of the dashboard.
package board

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nono/rook/internal/claude"
	"github.com/nono/rook/internal/github"
)

// Group is a row of the dashboard.
type Group string

// The groups, in display order.
const (
	Review   Group = "review"
	WIP      Group = "wip"
	Conflict Group = "conflict"
	Waiting  Group = "waiting"
	Team     Group = "team"
)

var order = []Group{Review, WIP, Conflict, Waiting, Team}

// CI is the state of the checks of a PR.
type CI string

// The CI states.
const (
	CIGreen   CI = "green"
	CIRed     CI = "red"
	CIPending CI = "pending"
	CINone    CI = "none"
)

// ReviewerState is the state shown on the avatar of a reviewer.
type ReviewerState string

// The reviewer states.
const (
	Approved  ReviewerState = "approved"
	Changes   ReviewerState = "changes"
	Commented ReviewerState = "commented"
	Dismissed ReviewerState = "dismissed"
	Requested ReviewerState = "requested"
)

// Reviewer is a person or a team on the card of a PR.
type Reviewer struct {
	Login     string
	AvatarURL string
	Team      bool
	State     ReviewerState
}

// Card is one item of the dashboard.
type Card struct {
	Kind      string // "pr"; step 2 adds "story"
	Repo      string
	Number    int
	Title     string // without the [sc-NNNN] prefix
	URL       string
	StoryID   string // "" when none
	StoryURL  string // "" when no ID or no workspace
	Labels    []string
	CI        CI
	Author    github.Actor
	Mine      bool
	Reviewers []Reviewer
	UpdatedAt time.Time
	// SessionDirs are the directories of the Claude Code sessions on the PR's branch.
	SessionDirs []string
}

// Section is a group with its cards.
type Section struct {
	Group Group
	Cards []Card
}

// RepoError tells that the PRs of a repo could not be read.
type RepoError struct {
	Repo string
	Err  string
}

// Board is the whole dashboard.
type Board struct {
	Sections  []Section
	Errors    []RepoError
	FetchedAt time.Time
	ShowRepo  bool // more than one repo: each card shows its repo
}

// Options are the inputs of the rules other than the PRs.
type Options struct {
	Me                string
	ShortcutWorkspace string
	Ignore            []string
	Sessions          []claude.Session
}

// ciOf maps a statusCheckRollup state to a CI state. An unknown state is
// pending, so that a new state cannot hide a broken PR.
func ciOf(state string) CI {
	switch state {
	case "SUCCESS":
		return CIGreen
	case "FAILURE", "ERROR":
		return CIRed
	case "PENDING", "EXPECTED":
		return CIPending
	case "":
		return CINone
	}
	return CIPending
}

// person tells if a is a person: not a bot or other non-User account, and not in the ignore list.
// GitHub logins do not depend on case.
func person(a github.Actor, opts Options) bool {
	return !a.Bot && !slices.ContainsFunc(opts.Ignore, func(l string) bool { return strings.EqualFold(l, a.Login) })
}

func human(a github.Actor, pr github.PR, opts Options) bool {
	return person(a, opts) && a.Login != pr.Author.Login
}

// sortedReviews gives the reviews of the PR, oldest first.
func sortedReviews(pr github.PR) []github.Review {
	out := slices.Clone(pr.Reviews)
	slices.SortStableFunc(out, func(a, b github.Review) int { return a.SubmittedAt.Compare(b.SubmittedAt) })
	return out
}

// classify gives the group of a PR and the reason label ("" for team and waiting).
// session tells that a Claude Code session is open on the PR's branch.
func classify(pr github.PR, opts Options, session bool) (Group, string) {
	if pr.Author.Login != opts.Me {
		var last *github.Review
		verdict := ""
		for _, r := range sortedReviews(pr) {
			if r.Author.Login != opts.Me {
				continue
			}
			last = &r
			if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" {
				verdict = r.State
			}
		}
		if slices.ContainsFunc(pr.RequestedUsers, func(a github.Actor) bool { return a.Login == opts.Me }) {
			if last == nil {
				return Review, "not reviewed"
			}
			return Review, "requested again"
		}
		if last != nil && verdict != "APPROVED" && last.CommitOID != pr.HeadOID {
			return Review, "new commits"
		}
		return Team, ""
	}
	switch ciOf(pr.CIState) {
	case CIGreen, CINone:
	case CIRed:
		return WIP, "CI red"
	case CIPending:
		return WIP, "CI pending"
	}
	hasHuman := slices.ContainsFunc(pr.Reviews, func(r github.Review) bool { return human(r.Author, pr, opts) })
	hasRequest := len(pr.RequestedTeams) > 0 ||
		slices.ContainsFunc(pr.RequestedUsers, func(a github.Actor) bool { return person(a, opts) })
	if !hasRequest && !hasHuman {
		return WIP, "no reviewer"
	}
	if session {
		return WIP, "Claude"
	}
	if pr.Mergeable == "CONFLICTING" {
		return Conflict, "conflict"
	}
	return Waiting, ""
}

// reviewers gives the human reviewers of a PR, in order of first review, then the requested ones.
// The state is the reviewer's verdict, else "commented", else "dismissed"; a pending request wins.
func reviewers(pr github.PR, opts Options) []Reviewer {
	var out []Reviewer
	index := map[string]int{}
	add := func(a github.Actor) int {
		i, ok := index[a.Login]
		if !ok {
			i = len(out)
			index[a.Login] = i
			out = append(out, Reviewer{Login: a.Login, AvatarURL: a.AvatarURL, State: Dismissed})
		}
		return i
	}
	for _, r := range sortedReviews(pr) {
		if !human(r.Author, pr, opts) {
			continue
		}
		i := add(r.Author)
		switch {
		case r.State == "APPROVED":
			out[i].State = Approved
		case r.State == "CHANGES_REQUESTED":
			out[i].State = Changes
		case r.State == "COMMENTED" && out[i].State == Dismissed:
			out[i].State = Commented
		}
	}
	for _, u := range pr.RequestedUsers {
		if human(u, pr, opts) {
			out[add(u)].State = Requested
		}
	}
	for _, t := range pr.RequestedTeams {
		out = append(out, Reviewer{Login: t, Team: true, State: Requested})
	}
	return out
}

var (
	titleStory  = regexp.MustCompile(`^\s*\[sc-(\d+)\]\s*`)
	branchStory = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])sc-(\d+)`)
)

// storyID finds the Shortcut story of a PR in its title prefix, else in its branch name.
// cleanTitle is the title without the prefix.
func storyID(title, branch string) (id, cleanTitle string) {
	if m := titleStory.FindStringSubmatch(title); m != nil {
		return m[1], title[len(m[0]):]
	}
	if m := branchStory.FindStringSubmatch(branch); m != nil {
		return m[1], title
	}
	return "", title
}

// sessionDirs gives the directories of the sessions on the repo (configured or
// current name) and head branch of the PR. GitHub repo names do not depend on
// case; git branch names do.
// A fork PR has none: its head branch is not in the repo of the session.
func sessionDirs(pr github.PR, sessions []claude.Session) []string {
	if pr.Fork {
		return nil
	}
	var dirs []string
	for _, s := range sessions {
		sameRepo := strings.EqualFold(s.Repo, pr.Repo) || strings.EqualFold(s.Repo, pr.CurrentRepo)
		if sameRepo && s.Branch == pr.HeadRefName {
			dirs = append(dirs, s.Dir)
		}
	}
	return dirs
}

// Build sorts the PRs into the five sections, in display order, newest first in each.
func Build(prs []github.PR, opts Options) []Section {
	byGroup := map[Group][]Card{}
	for _, pr := range prs {
		dirs := sessionDirs(pr, opts.Sessions)
		g, reason := classify(pr, opts, len(dirs) > 0)
		id, title := storyID(pr.Title, pr.HeadRefName)
		c := Card{
			Kind: "pr", Repo: pr.Repo, Number: pr.Number,
			Title: title, URL: pr.URL, StoryID: id, CI: ciOf(pr.CIState),
			Author: pr.Author, Mine: pr.Author.Login == opts.Me,
			Reviewers: reviewers(pr, opts), UpdatedAt: pr.UpdatedAt,
			SessionDirs: dirs,
		}
		if id != "" && opts.ShortcutWorkspace != "" {
			c.StoryURL = "https://app.shortcut.com/" + opts.ShortcutWorkspace + "/story/" + id
		}
		if reason != "" {
			c.Labels = append(c.Labels, reason)
		}
		if pr.IsDraft {
			c.Labels = append(c.Labels, "draft")
		}
		if pr.Mergeable == "UNKNOWN" {
			c.Labels = append(c.Labels, "merge ?")
		}
		byGroup[g] = append(byGroup[g], c)
	}
	out := make([]Section, 0, len(order))
	for _, g := range order {
		cards := byGroup[g]
		slices.SortStableFunc(cards, func(a, b Card) int {
			return cmp.Or(b.UpdatedAt.Compare(a.UpdatedAt), cmp.Compare(b.Number, a.Number))
		})
		out = append(out, Section{Group: g, Cards: cards})
	}
	return out
}
