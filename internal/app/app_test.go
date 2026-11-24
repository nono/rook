package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nono/rook/internal/board"
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
	b := Load(context.Background(), fake{"o/a": {pr("o/a")}}, "me", cfg, func() time.Time { return now })
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
	b := Load(context.Background(), prefixed{}, "me", config.Config{Repos: []string{"o/b"}}, time.Now)
	if len(b.Errors) != 1 || b.Errors[0].Err != "boom" {
		t.Fatalf("errors = %+v", b.Errors)
	}
}

func TestLoadShowRepo(t *testing.T) {
	f := fake{"o/a": {pr("o/a")}, "o/b": {pr("o/b")}}
	two := Load(context.Background(), f, "me", config.Config{Repos: []string{"o/a", "o/b"}}, time.Now)
	for _, c := range cards(two) {
		if !c.ShowRepo {
			t.Errorf("two repos: ShowRepo false on %+v", c)
		}
	}
	one := Load(context.Background(), f, "me", config.Config{Repos: []string{"o/a"}}, time.Now)
	for _, c := range cards(one) {
		if c.ShowRepo {
			t.Errorf("one repo: ShowRepo true on %+v", c)
		}
	}
}

func TestLoadOptions(t *testing.T) {
	p := pr("o/a")
	p.Title = "[sc-5] X"
	p.Reviews = []github.Review{{Author: github.Actor{Login: "rabbit"}, State: "COMMENTED"}}
	cfg := config.Config{Repos: []string{"o/a"}, ShortcutWorkspace: "ws", IgnoreReviewers: []string{"rabbit"}}
	c := cards(Load(context.Background(), fake{"o/a": {p}}, "me", cfg, time.Now))[0]
	if c.StoryURL != "https://app.shortcut.com/ws/story/5" || len(c.Reviewers) != 0 || c.Mine {
		t.Fatalf("card = %+v", c)
	}
	p.Author.Login = "me"
	if c := cards(Load(context.Background(), fake{"o/a": {p}}, "me", cfg, time.Now))[0]; !c.Mine {
		t.Fatalf("my PR: card = %+v", c)
	}
}

func TestLoadFetchedAt(t *testing.T) {
	b := Load(context.Background(), fake{}, "me", config.Config{Repos: []string{"o/a"}}, func() time.Time { return now })
	if !b.FetchedAt.Equal(now) {
		t.Fatalf("FetchedAt = %s", b.FetchedAt)
	}
}

// blocking returns a PR once all the expected calls have started, else an error after 1 s.
type blocking struct {
	started sync.WaitGroup
	t       *testing.T
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
	f := &blocking{t: t}
	f.started.Add(2)
	b := Load(context.Background(), f, "me", config.Config{Repos: []string{"o/a", "o/b"}}, time.Now)
	if len(b.Errors) != 0 {
		t.Fatalf("errors = %+v", b.Errors)
	}
}
