package board

import (
	"reflect"
	"testing"
	"time"

	"github.com/nono/rook/internal/github"
)

var t0 = time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)

func user(login string) github.Actor { return github.Actor{Login: login} }

func rev(login, state, commit string, minute int) github.Review {
	return github.Review{Author: user(login), State: state, CommitOID: commit, SubmittedAt: t0.Add(time.Duration(minute) * time.Minute)}
}

// pr builds a PR by "other", head h2, CI green, mergeable, with mods applied.
func pr(mods ...func(*github.PR)) github.PR {
	p := github.PR{
		Repo: "o/r", Number: 1, Title: "T", URL: "https://github.com/o/r/pull/1",
		UpdatedAt: t0, HeadOID: "h2", Mergeable: "MERGEABLE",
		Author: user("other"), CIState: "SUCCESS",
	}
	for _, m := range mods {
		m(&p)
	}
	return p
}

func mine(p *github.PR)  { p.Author = user("me") }
func draft(p *github.PR) { p.IsDraft = true }
func requested(logins ...string) func(*github.PR) {
	return func(p *github.PR) {
		for _, l := range logins {
			p.RequestedUsers = append(p.RequestedUsers, user(l))
		}
	}
}
func reviews(rs ...github.Review) func(*github.PR) {
	return func(p *github.PR) { p.Reviews = append(p.Reviews, rs...) }
}
func ci(state string) func(*github.PR)        { return func(p *github.PR) { p.CIState = state } }
func mergeable(state string) func(*github.PR) { return func(p *github.PR) { p.Mergeable = state } }

func TestClassify(t *testing.T) {
	bot := rev("rabbit[bot]", "COMMENTED", "h2", 1)
	bot.Author.Bot = true
	cases := []struct {
		name   string
		pr     github.PR
		ignore []string
		group  Group
		reason string
	}{
		{"requested, never reviewed", pr(requested("me")), nil, Review, "not reviewed"},
		{"approved, requested again", pr(reviews(rev("me", "APPROVED", "h1", 1)), requested("me")), nil, Review, "requested again"},
		{"commented at h1, head h2", pr(reviews(rev("me", "COMMENTED", "h1", 1))), nil, Review, "new commits"},
		{"changes at h1, head h2", pr(reviews(rev("me", "CHANGES_REQUESTED", "h1", 1))), nil, Review, "new commits"},
		{"approved at h1, head h2", pr(reviews(rev("me", "APPROVED", "h1", 1))), nil, Team, ""},
		{"approved h1, reply h1, head h2", pr(reviews(rev("me", "APPROVED", "h1", 1), rev("me", "COMMENTED", "h1", 2))), nil, Team, ""},
		{"changes h1, reply h2, head h2", pr(reviews(rev("me", "CHANGES_REQUESTED", "h1", 1), rev("me", "COMMENTED", "h2", 2))), nil, Team, ""},
		{"reviews out of order", pr(reviews(rev("me", "COMMENTED", "h2", 2), rev("me", "CHANGES_REQUESTED", "h1", 1))), nil, Team, ""},
		{"dismissed at h1, head h2", pr(reviews(rev("me", "DISMISSED", "h1", 1))), nil, Review, "new commits"},
		{"not requested, never reviewed", pr(), nil, Team, ""},
		{"draft by someone else, requested", pr(draft, requested("me")), nil, Review, "not reviewed"},
		{"someone else requested", pr(requested("alice")), nil, Team, ""},

		{"mine: CI FAILURE + CONFLICTING", pr(mine, requested("alice"), ci("FAILURE"), mergeable("CONFLICTING")), nil, WIP, "CI red"},
		{"mine: CI ERROR", pr(mine, requested("alice"), ci("ERROR")), nil, WIP, "CI red"},
		{"mine: CI PENDING", pr(mine, requested("alice"), ci("PENDING")), nil, WIP, "CI pending"},
		{"mine: CI EXPECTED", pr(mine, requested("alice"), ci("EXPECTED")), nil, WIP, "CI pending"},
		{"mine: no CI, requested user", pr(mine, requested("alice"), ci("")), nil, Waiting, ""},
		{"mine: no requests, only bot review", pr(mine, reviews(bot)), nil, WIP, "no reviewer"},
		{"mine: no requests, only my own review", pr(mine, reviews(rev("me", "COMMENTED", "h2", 1))), nil, WIP, "no reviewer"},
		{"mine: no requests, ignored login", pr(mine, reviews(rev("rabbit", "COMMENTED", "h2", 1))), []string{"rabbit"}, WIP, "no reviewer"},
		{"mine: team requested only", pr(mine, func(p *github.PR) { p.RequestedTeams = []string{"core"} }), nil, Waiting, ""},
		{"mine: human review, no request, CONFLICTING", pr(mine, reviews(rev("alice", "COMMENTED", "h2", 1)), mergeable("CONFLICTING")), nil, Conflict, "conflict"},
		{"mine: changes requested, CI green", pr(mine, reviews(rev("alice", "CHANGES_REQUESTED", "h2", 1))), nil, Waiting, ""},
		{"mine: draft, CI green, requested", pr(mine, draft, requested("alice")), nil, Waiting, ""},
		{"mine: UNKNOWN mergeable, requested", pr(mine, requested("alice"), mergeable("UNKNOWN")), nil, Waiting, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, reason := Classify(c.pr, Options{Me: "me", Ignore: c.ignore})
			if g != c.group || reason != c.reason {
				t.Fatalf("got (%s, %q), want (%s, %q)", g, reason, c.group, c.reason)
			}
		})
	}
}

func TestCIOf(t *testing.T) {
	for in, want := range map[string]CI{
		"SUCCESS": CIGreen, "FAILURE": CIRed, "ERROR": CIRed,
		"PENDING": CIPending, "EXPECTED": CIPending, "": CINone,
	} {
		if got := CIOf(in); got != want {
			t.Errorf("CIOf(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestReviewers(t *testing.T) {
	bot := rev("rabbit[bot]", "COMMENTED", "h2", 5)
	bot.Author.Bot = true
	p := pr(
		reviews(
			rev("alice", "APPROVED", "h1", 1),
			rev("bob", "CHANGES_REQUESTED", "h1", 2),
			rev("carol", "COMMENTED", "h1", 3),
			rev("alice", "COMMENTED", "h2", 4),
			bot,
			rev("other", "COMMENTED", "h2", 6),
		),
		requested("bob", "dave"),
		func(p *github.PR) { p.RequestedTeams = []string{"core"} },
	)
	got := Reviewers(p, Options{Me: "me"})
	want := []Reviewer{
		{Login: "alice", State: Approved},
		{Login: "bob", State: Requested},
		{Login: "carol", State: Commented},
		{Login: "dave", State: Requested},
		{Login: "core", Team: true, State: Requested},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestReviewersChangesVerdict(t *testing.T) {
	p := pr(reviews(rev("bob", "CHANGES_REQUESTED", "h1", 1), rev("bob", "COMMENTED", "h2", 2)))
	got := Reviewers(p, Options{Me: "me"})
	want := []Reviewer{{Login: "bob", State: Changes}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStoryID(t *testing.T) {
	cases := []struct{ title, branch, id, clean string }{
		{"[sc-1234] Fix the parser", "x", "1234", "Fix the parser"},
		{"Add a flag", "alice/sc-12-flag", "12", "Add a flag"},
		{"Fix disc-5", "misc-5", "", "Fix disc-5"},
		{"Fix", "sc-7", "7", "Fix"},
		{"Fix", "SC-8-x", "8", "Fix"},
	}
	for _, c := range cases {
		id, clean := StoryID(c.title, c.branch)
		if id != c.id || clean != c.clean {
			t.Errorf("StoryID(%q, %q) = (%q, %q), want (%q, %q)", c.title, c.branch, id, clean, c.id, c.clean)
		}
	}
}

func TestBuild(t *testing.T) {
	review := pr(requested("me"), func(p *github.PR) { p.Number = 1; p.Title = "[sc-9] Do it" })
	older := pr(func(p *github.PR) { p.Number = 2 })
	newer := pr(func(p *github.PR) { p.Number = 3; p.UpdatedAt = t0.Add(time.Hour) })
	wip := pr(mine, draft, requested("alice"), ci("FAILURE"), mergeable("UNKNOWN"), func(p *github.PR) { p.Number = 4 })

	secs := Build([]github.PR{older, review, wip, newer}, Options{Me: "me", ShortcutWorkspace: "ws", ShowRepo: true})

	want := []struct {
		g                 Group
		name, hint, color string
	}{
		{Review, "Review", "your turn to review", "#ea580c"},
		{WIP, "WIP", "your turn to fix", "#ca8a04"},
		{Conflict, "Conflict", "rebase needed", "#dc2626"},
		{Waiting, "Waiting", "others' turn", "#2563eb"},
		{Team, "Team", "FYI", "#6b7280"},
	}
	if len(secs) != len(want) {
		t.Fatalf("got %d sections, want 5", len(secs))
	}
	for i, w := range want {
		s := secs[i]
		if s.Group != w.g || s.Name != w.name || s.Hint != w.hint || s.Color != w.color {
			t.Errorf("section %d = %+v, want %+v", i, s, w)
		}
	}
	if len(secs[2].Cards) != 0 || len(secs[3].Cards) != 0 {
		t.Errorf("conflict and waiting should be empty")
	}
	team := secs[4].Cards
	if len(team) != 2 || team[0].Number != 3 || team[1].Number != 2 {
		t.Fatalf("team cards not newest first: %+v", team)
	}
	c := secs[0].Cards[0]
	if c.Kind != "pr" || c.StoryID != "9" || c.StoryURL != "https://app.shortcut.com/ws/story/9" || c.Title != "Do it" {
		t.Errorf("review card = %+v", c)
	}
	if !c.ShowRepo || c.Mine || c.Group != Review {
		t.Errorf("review card flags = %+v", c)
	}
	if want := []Label{{"not reviewed", "gry"}}; !reflect.DeepEqual(c.Labels, want) {
		t.Errorf("review labels = %+v, want %+v", c.Labels, want)
	}
	w := secs[1].Cards[0]
	if want := []Label{{"CI red", "red"}, {"draft", "gry"}, {"merge ?", "gry"}}; !reflect.DeepEqual(w.Labels, want) {
		t.Errorf("wip labels = %+v, want %+v", w.Labels, want)
	}
	if !w.Mine || w.CI != CIRed {
		t.Errorf("wip card = %+v", w)
	}

	noWS := Build([]github.PR{review}, Options{Me: "me"})
	if c := noWS[0].Cards[0]; c.StoryID != "9" || c.StoryURL != "" || c.ShowRepo {
		t.Errorf("without workspace: %+v", c)
	}
}

func TestBuildLabelTones(t *testing.T) {
	cases := map[string]github.PR{
		"conflict":    pr(mine, reviews(rev("alice", "COMMENTED", "h2", 1)), mergeable("CONFLICTING")),
		"CI pending":  pr(mine, requested("alice"), ci("PENDING")),
		"no reviewer": pr(mine),
		"new commits": pr(reviews(rev("me", "COMMENTED", "h1", 1))),
	}
	tones := map[string]string{"conflict": "red", "CI pending": "ylw", "no reviewer": "ylw", "new commits": "gry"}
	for reason, p := range cases {
		var got []Label
		for _, s := range Build([]github.PR{p}, Options{Me: "me"}) {
			for _, c := range s.Cards {
				got = c.Labels
			}
		}
		if want := []Label{{reason, tones[reason]}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: labels %+v, want %+v", reason, got, want)
		}
	}
}
