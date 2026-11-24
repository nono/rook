package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/claude"
	"github.com/nono/rook/internal/config"
	"github.com/nono/rook/internal/github"
)

var now = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

type fake map[string][]github.PR

func (f fake) OpenPRs(_ context.Context, repo string) ([]github.PR, error) {
	if prs, ok := f[repo]; ok {
		return prs, nil
	}
	return nil, errors.New("boom")
}

func cards(b board.Board) []board.Card {
	var out []board.Card
	for _, s := range b.Sections {
		out = append(out, s.Cards...)
	}
	return out
}

func pr(repo string) github.PR {
	return github.PR{Repo: repo, Number: 1, Author: github.Actor{Login: "other"}, CIState: "SUCCESS", Mergeable: "MERGEABLE"}
}

func TestLoadKeepsOtherReposWhenOneFails(t *testing.T) {
	cfg := config.Config{Repos: []string{"o/a", "o/b"}}
	b := Load(context.Background(), fake{"o/a": {pr("o/a")}}, nil, "me", cfg, func() time.Time { return now })
	if got := cards(b); len(got) != 1 || got[0].Repo != "o/a" {
		t.Fatalf("cards = %+v", got)
	}
	if len(b.Errors) != 1 || b.Errors[0].Repo != "o/b" || !strings.Contains(b.Errors[0].Err, "boom") {
		t.Fatalf("errors = %+v", b.Errors)
	}
}

type prefixed struct{}

func (prefixed) OpenPRs(_ context.Context, repo string) ([]github.PR, error) {
	return nil, errors.New(repo + ": boom")
}

func TestLoadErrorNamesRepoOnce(t *testing.T) {
	b := Load(context.Background(), prefixed{}, nil, "me", config.Config{Repos: []string{"o/b"}}, time.Now)
	if len(b.Errors) != 1 || b.Errors[0].Err != "boom" {
		t.Fatalf("errors = %+v", b.Errors)
	}
}

func TestLoadShowRepo(t *testing.T) {
	f := fake{"o/a": {pr("o/a")}, "o/b": {pr("o/b")}}
	if two := Load(context.Background(), f, nil, "me", config.Config{Repos: []string{"o/a", "o/b"}}, time.Now); !two.ShowRepo {
		t.Error("two repos: ShowRepo false")
	}
	if one := Load(context.Background(), f, nil, "me", config.Config{Repos: []string{"o/a"}}, time.Now); one.ShowRepo {
		t.Error("one repo: ShowRepo true")
	}
}

func TestLoadOptions(t *testing.T) {
	p := pr("o/a")
	p.Title = "[sc-5] X"
	p.Reviews = []github.Review{{Author: github.Actor{Login: "rabbit"}, State: "COMMENTED"}}
	cfg := config.Config{Repos: []string{"o/a"}, ShortcutWorkspace: "ws", IgnoreReviewers: []string{"rabbit"}}
	c := cards(Load(context.Background(), fake{"o/a": {p}}, nil, "me", cfg, time.Now))[0]
	if c.StoryURL != "https://app.shortcut.com/ws/story/5" || len(c.Reviewers) != 0 || c.Mine {
		t.Fatalf("card = %+v", c)
	}
	p.Author.Login = "me"
	if c := cards(Load(context.Background(), fake{"o/a": {p}}, nil, "me", cfg, time.Now))[0]; !c.Mine {
		t.Fatalf("my PR: card = %+v", c)
	}
}

func TestLoadFetchedAt(t *testing.T) {
	b := Load(context.Background(), fake{}, nil, "me", config.Config{Repos: []string{"o/a"}}, func() time.Time { return now })
	if !b.FetchedAt.Equal(now) {
		t.Fatalf("FetchedAt = %s", b.FetchedAt)
	}
}

// blocking returns a PR once all the expected calls have started, else an error after 1 s.
type blocking struct {
	started sync.WaitGroup
}

func (b *blocking) OpenPRs(_ context.Context, repo string) ([]github.PR, error) {
	b.started.Done()
	done := make(chan struct{})
	go func() { b.started.Wait(); close(done) }()
	select {
	case <-done:
		return []github.PR{pr(repo)}, nil
	case <-time.After(time.Second):
		return nil, errors.New("not parallel")
	}
}

func TestLoadParallel(t *testing.T) {
	f := &blocking{}
	f.started.Add(2)
	b := Load(context.Background(), f, nil, "me", config.Config{Repos: []string{"o/a", "o/b"}}, time.Now)
	if len(b.Errors) != 0 {
		t.Fatalf("errors = %+v", b.Errors)
	}
}

func TestLoadUsesSessions(t *testing.T) {
	p := pr("o/a")
	p.Author.Login = "me"
	p.HeadRefName = "x"
	p.RequestedUsers = []github.Actor{{Login: "alice"}}
	sessions := func(context.Context) []claude.Session { return []claude.Session{{Dir: "/w", Repo: "o/a", Branch: "x"}} }
	b := Load(context.Background(), fake{"o/a": {p}}, sessions, "me", config.Config{Repos: []string{"o/a"}}, time.Now)
	wip := b.Sections[1]
	if wip.Group != board.WIP || len(wip.Cards) != 1 || len(wip.Cards[0].Claude) != 1 || wip.Cards[0].Claude[0] != "/w" {
		t.Fatalf("WIP section = %+v", wip)
	}
}

func TestLoadSessionsInParallel(t *testing.T) {
	f := &blocking{}
	f.started.Add(2)
	parallel := false
	sessions := func(context.Context) []claude.Session {
		f.started.Done()
		done := make(chan struct{})
		go func() { f.started.Wait(); close(done) }()
		select {
		case <-done:
			parallel = true
		case <-time.After(time.Second):
		}
		return nil
	}
	b := Load(context.Background(), f, sessions, "me", config.Config{Repos: []string{"o/a"}}, time.Now)
	if len(b.Errors) != 0 || !parallel {
		t.Fatalf("errors = %+v, parallel = %v", b.Errors, parallel)
	}
}
