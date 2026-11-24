package board

import (
	"reflect"
	"testing"
	"time"

	"github.com/nono/rook/internal/claude"
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
		{"mine: only a bot requested", pr(mine, func(p *github.PR) { p.RequestedUsers = []github.Actor{{Login: "copilot", Bot: true}} }), nil, WIP, "no reviewer"},
		{"mine: only an ignored login requested", pr(mine, requested("rabbit")), []string{"rabbit"}, WIP, "no reviewer"},
		{"mine: ignored login, other case", pr(mine, reviews(rev("rabbit", "COMMENTED", "h2", 1))), []string{"Rabbit"}, WIP, "no reviewer"},
		{"mine: no reviewer and CONFLICTING", pr(mine, mergeable("CONFLICTING")), nil, WIP, "no reviewer"},
		{"team request with my login is not a request to me", pr(func(p *github.PR) { p.RequestedTeams = []string{"me"} }), nil, Team, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, reason := classify(c.pr, Options{Me: "me", Ignore: c.ignore}, false)
			if g != c.group || reason != c.reason {
				t.Fatalf("got (%s, %q), want (%s, %q)", g, reason, c.group, c.reason)
			}
		})
	}
}

func TestClassifyClaude(t *testing.T) {
	cases := []struct {
		name   string
		pr     github.PR
		group  Group
		reason string
	}{
		{"mine: session, waiting", pr(mine, requested("alice")), WIP, "Claude"},
		{"mine: session + CONFLICTING", pr(mine, requested("alice"), mergeable("CONFLICTING")), WIP, "Claude"},
		{"mine: session + CI red", pr(mine, requested("alice"), ci("FAILURE")), WIP, "CI red"},
		{"mine: session + no reviewer", pr(mine), WIP, "no reviewer"},
		{"session, requested from me", pr(requested("me")), Review, "not reviewed"},
		{"session, someone else's PR", pr(), Team, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, reason := classify(c.pr, Options{Me: "me"}, true)
			if g != c.group || reason != c.reason {
				t.Fatalf("got (%s, %q), want (%s, %q)", g, reason, c.group, c.reason)
			}
		})
	}
}

func TestBuildSessions(t *testing.T) {
	head := func(b string) func(*github.PR) { return func(p *github.PR) { p.HeadRefName = b } }
	p1 := pr(mine, requested("alice"), head("sc-1/a"))
	p2 := pr(head("b"), func(p *github.PR) { p.Number = 2 })
	find := func(secs []Section, n int) (Group, Card) {
		for _, s := range secs {
			for _, c := range s.Cards {
				if c.Number == n {
					return s.Group, c
				}
			}
		}
		t.Fatalf("no card #%d", n)
		return "", Card{}
	}

	secs := Build([]github.PR{p1, p2}, Options{Me: "me", Sessions: []claude.Session{
		{Dir: "/w/1", Repo: "O/R", Branches: []string{"sc-1/a"}},
		{Dir: "/w/2", Repo: "o/r", Branches: []string{"main", "sc-1/a"}},
		{Dir: "/w/3", Repo: "o/r", Branches: []string{"B"}},
		{Dir: "/w/4", Repo: "o/x", Branches: []string{"b"}},
	}})
	g, c := find(secs, 1)
	if g != WIP || !reflect.DeepEqual(c.Labels, []string{"Claude"}) || !reflect.DeepEqual(c.Claude, []string{"/w/1", "/w/2"}) {
		t.Errorf("p1: group %s, card %+v", g, c)
	}
	if g, c := find(secs, 2); g != Team || c.Claude != nil {
		t.Errorf("p2: group %s, Claude %q; want team, nil", g, c.Claude)
	}

	secs = Build([]github.PR{p2}, Options{Me: "me", Sessions: []claude.Session{{Dir: "/w/5", Repo: "o/r", Branches: []string{"b"}}}})
	if g, c := find(secs, 2); g != Team || !reflect.DeepEqual(c.Claude, []string{"/w/5"}) {
		t.Errorf("p2 with session: group %s, Claude %q; want team, [/w/5]", g, c.Claude)
	}

	// A CI-red PR keeps its label and gets the session directories.
	red := pr(mine, requested("alice"), head("sc-1/a"), ci("FAILURE"), func(p *github.PR) { p.Number = 4 })
	secs = Build([]github.PR{red}, Options{Me: "me", Sessions: []claude.Session{{Dir: "/w/7", Repo: "o/r", Branches: []string{"sc-1/a"}}}})
	if g, c := find(secs, 4); g != WIP || !reflect.DeepEqual(c.Labels, []string{"CI red"}) || !reflect.DeepEqual(c.Claude, []string{"/w/7"}) {
		t.Errorf("CI-red PR: group %s, card %+v", g, c)
	}

	// The head branch of a fork PR is in the fork, not in the repo of the session.
	fork := pr(mine, requested("alice"), head("main"), func(p *github.PR) { p.Number = 3; p.Fork = true })
	secs = Build([]github.PR{fork}, Options{Me: "me", Sessions: []claude.Session{{Dir: "/w/6", Repo: "o/r", Branches: []string{"main"}}}})
	if g, c := find(secs, 3); g != Waiting || c.Claude != nil {
		t.Errorf("fork PR: group %s, Claude %q; want waiting, nil", g, c.Claude)
	}
}

func TestCIOf(t *testing.T) {
	for in, want := range map[string]CI{
		"SUCCESS": CIGreen, "FAILURE": CIRed, "ERROR": CIRed,
		"PENDING": CIPending, "EXPECTED": CIPending, "": CINone,
		"SOMETHING_NEW": CIPending,
	} {
		if got := ciOf(in); got != want {
			t.Errorf("ciOf(%q) = %s, want %s", in, got, want)
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
		requested("bob", "dave", "rabbit"),
		func(p *github.PR) {
			p.RequestedUsers = append(p.RequestedUsers, github.Actor{Login: "copilot", Bot: true})
		},
		func(p *github.PR) { p.RequestedTeams = []string{"core"} },
	)
	got := reviewers(p, Options{Me: "me", Ignore: []string{"Rabbit"}})
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

func TestReviewersDismissedOnly(t *testing.T) {
	p := pr(reviews(rev("bob", "DISMISSED", "h1", 1), rev("eve", "DISMISSED", "h1", 2), rev("eve", "COMMENTED", "h2", 3)))
	got := reviewers(p, Options{Me: "me"})
	want := []Reviewer{{Login: "bob", State: Dismissed}, {Login: "eve", State: Commented}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestReviewersChangesVerdict(t *testing.T) {
	p := pr(reviews(rev("bob", "CHANGES_REQUESTED", "h1", 1), rev("bob", "COMMENTED", "h2", 2)))
	got := reviewers(p, Options{Me: "me"})
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
		id, clean := storyID(c.title, c.branch)
		if id != c.id || clean != c.clean {
			t.Errorf("storyID(%q, %q) = (%q, %q), want (%q, %q)", c.title, c.branch, id, clean, c.id, c.clean)
		}
	}
}

func TestBuild(t *testing.T) {
	review := pr(requested("me"), func(p *github.PR) { p.Number = 1; p.Title = "[sc-9] Do it" })
	older := pr(func(p *github.PR) { p.Number = 3 })
	newer := pr(func(p *github.PR) { p.Number = 2; p.UpdatedAt = t0.Add(time.Hour) })
	wip := pr(mine, draft, requested("alice"), ci("FAILURE"), mergeable("UNKNOWN"), func(p *github.PR) { p.Number = 4 })

	secs := Build([]github.PR{older, review, wip, newer}, Options{Me: "me", ShortcutWorkspace: "ws"})

	var groups []Group
	for _, s := range secs {
		groups = append(groups, s.Group)
	}
	if want := []Group{Review, WIP, Conflict, Waiting, Team}; !reflect.DeepEqual(groups, want) {
		t.Fatalf("groups = %v, want %v", groups, want)
	}
	if len(secs[2].Cards) != 0 || len(secs[3].Cards) != 0 {
		t.Errorf("conflict and waiting should be empty")
	}
	team := secs[4].Cards
	if len(team) != 2 || team[0].Number != 2 || team[1].Number != 3 {
		t.Fatalf("team cards not newest first: %+v", team)
	}
	c := secs[0].Cards[0]
	if c.Kind != "pr" || c.StoryID != "9" || c.StoryURL != "https://app.shortcut.com/ws/story/9" || c.Title != "Do it" || c.Mine {
		t.Errorf("review card = %+v", c)
	}
	if want := []string{"not reviewed"}; !reflect.DeepEqual(c.Labels, want) {
		t.Errorf("review labels = %q, want %q", c.Labels, want)
	}
	w := secs[1].Cards[0]
	if want := []string{"CI red", "draft", "merge ?"}; !reflect.DeepEqual(w.Labels, want) {
		t.Errorf("wip labels = %q, want %q", w.Labels, want)
	}
	if !w.Mine || w.CI != CIRed {
		t.Errorf("wip card = %+v", w)
	}

	noWS := Build([]github.PR{review}, Options{Me: "me"})
	if c := noWS[0].Cards[0]; c.StoryID != "9" || c.StoryURL != "" {
		t.Errorf("without workspace: %+v", c)
	}
}

func TestBuildSameTimeHigherNumberFirst(t *testing.T) {
	low := pr(func(p *github.PR) { p.Number = 5 })
	high := pr(func(p *github.PR) { p.Number = 7 })
	team := Build([]github.PR{low, high}, Options{Me: "me"})[4].Cards
	if team[0].Number != 7 || team[1].Number != 5 {
		t.Fatalf("order = %d, %d; want 7, 5", team[0].Number, team[1].Number)
	}
}
