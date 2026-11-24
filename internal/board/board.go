// Package board sorts pull requests into the groups of the dashboard.
package board

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nono/rook/internal/github"
)

// Group is a row of the dashboard.
type Group string

// The groups, see Order for the display order.
const (
	Review   Group = "review"
	WIP      Group = "wip"
	Conflict Group = "conflict"
	Waiting  Group = "waiting"
	Team     Group = "team"
)

// Order is the display order of the groups.
var Order = []Group{Review, WIP, Conflict, Waiting, Team}

var groupInfo = map[Group]struct{ name, hint, color string }{
	Review:   {"Review", "your turn to review", "#ea580c"},
	WIP:      {"WIP", "your turn to fix", "#ca8a04"},
	Conflict: {"Conflict", "rebase needed", "#dc2626"},
	Waiting:  {"Waiting", "others' turn", "#2563eb"},
	Team:     {"Team", "FYI", "#6b7280"},
}

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
	Requested ReviewerState = "requested"
)

// Reviewer is a person or a team on the card of a PR.
type Reviewer struct {
	Login     string
	AvatarURL string
	Team      bool
	State     ReviewerState
}

// Label is a small tag on a card. Tone is "red", "ylw" or "gry".
type Label struct {
	Text string
	Tone string
}

// Card is one item of the dashboard.
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

// Section is a group with its cards.
type Section struct {
	Group Group
	Name  string
	Hint  string
	Color string
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
}

// Options are the inputs of the rules other than the PRs.
type Options struct {
	Me                string
	ShortcutWorkspace string
	Ignore            []string
	ShowRepo          bool
}

// CIOf maps a statusCheckRollup state to a CI state.
func CIOf(state string) CI {
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

// sortedReviews gives the reviews of login, oldest first.
func sortedReviews(pr github.PR, login string) []github.Review {
	var out []github.Review
	for _, r := range pr.Reviews {
		if r.Author.Login == login {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b github.Review) int { return a.SubmittedAt.Compare(b.SubmittedAt) })
	return out
}

// verdict gives the state of the last APPROVED or CHANGES_REQUESTED review, "" if none.
func verdict(rs []github.Review) string {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].State == "APPROVED" || rs[i].State == "CHANGES_REQUESTED" {
			return rs[i].State
		}
	}
	return ""
}

func requestedUser(pr github.PR, login string) bool {
	return slices.ContainsFunc(pr.RequestedUsers, func(a github.Actor) bool { return a.Login == login })
}

// Classify gives the group of a PR and the reason label ("" for team and waiting).
func Classify(pr github.PR, opts Options) (Group, string) {
	if pr.Author.Login != opts.Me {
		mine := sortedReviews(pr, opts.Me)
		if requestedUser(pr, opts.Me) {
			if len(mine) == 0 {
				return Review, "not reviewed"
			}
			return Review, "requested again"
		}
		if len(mine) > 0 && verdict(mine) != "APPROVED" && mine[len(mine)-1].CommitOID != pr.HeadOID {
			return Review, "new commits"
		}
		return Team, ""
	}
	switch CIOf(pr.CIState) {
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
	if pr.Mergeable == "CONFLICTING" {
		return Conflict, "conflict"
	}
	return Waiting, ""
}

// Reviewers gives the human reviewers of a PR, in order of first review, then the requested ones.
func Reviewers(pr github.PR, opts Options) []Reviewer {
	all := slices.Clone(pr.Reviews)
	slices.SortStableFunc(all, func(a, b github.Review) int { return a.SubmittedAt.Compare(b.SubmittedAt) })
	var out []Reviewer
	index := map[string]int{}
	for _, r := range all {
		if !human(r.Author, pr, opts) {
			continue
		}
		if _, ok := index[r.Author.Login]; ok {
			continue
		}
		index[r.Author.Login] = len(out)
		state := Commented
		switch verdict(sortedReviews(pr, r.Author.Login)) {
		case "APPROVED":
			state = Approved
		case "CHANGES_REQUESTED":
			state = Changes
		}
		out = append(out, Reviewer{Login: r.Author.Login, AvatarURL: r.Author.AvatarURL, State: state})
	}
	for _, u := range pr.RequestedUsers {
		if i, ok := index[u.Login]; ok {
			out[i].State = Requested
			continue
		}
		if !person(u, opts) {
			continue
		}
		index[u.Login] = len(out)
		out = append(out, Reviewer{Login: u.Login, AvatarURL: u.AvatarURL, State: Requested})
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

// StoryID finds the Shortcut story of a PR in its title prefix, else in its branch name.
// cleanTitle is the title without the prefix.
func StoryID(title, branch string) (id, cleanTitle string) {
	if m := titleStory.FindStringSubmatch(title); m != nil {
		return m[1], title[len(m[0]):]
	}
	if m := branchStory.FindStringSubmatch(branch); m != nil {
		return m[1], title
	}
	return "", title
}

var reasonTone = map[string]string{"CI red": "red", "conflict": "red", "CI pending": "ylw", "no reviewer": "ylw"}

// Build sorts the PRs into the five sections, in Order, newest first in each.
func Build(prs []github.PR, opts Options) []Section {
	byGroup := map[Group][]Card{}
	for _, pr := range prs {
		g, reason := Classify(pr, opts)
		id, title := StoryID(pr.Title, pr.HeadRefName)
		c := Card{
			Kind: "pr", Repo: pr.Repo, ShowRepo: opts.ShowRepo, Number: pr.Number,
			Title: title, URL: pr.URL, StoryID: id, Group: g, CI: CIOf(pr.CIState),
			Author: pr.Author, Mine: pr.Author.Login == opts.Me,
			Reviewers: Reviewers(pr, opts), UpdatedAt: pr.UpdatedAt,
		}
		if id != "" && opts.ShortcutWorkspace != "" {
			c.StoryURL = "https://app.shortcut.com/" + opts.ShortcutWorkspace + "/story/" + id
		}
		if reason != "" {
			c.Labels = append(c.Labels, Label{reason, cmp.Or(reasonTone[reason], "gry")})
		}
		if pr.IsDraft {
			c.Labels = append(c.Labels, Label{"draft", "gry"})
		}
		if pr.Mergeable == "UNKNOWN" {
			c.Labels = append(c.Labels, Label{"merge ?", "gry"})
		}
		byGroup[g] = append(byGroup[g], c)
	}
	out := make([]Section, 0, len(Order))
	for _, g := range Order {
		cards := byGroup[g]
		slices.SortStableFunc(cards, func(a, b Card) int {
			return cmp.Or(b.UpdatedAt.Compare(a.UpdatedAt), cmp.Compare(b.Number, a.Number))
		})
		info := groupInfo[g]
		out = append(out, Section{Group: g, Name: info.name, Hint: info.hint, Color: info.color, Cards: cards})
	}
	return out
}
