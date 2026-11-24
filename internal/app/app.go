// Package app reads the PRs of all the repos and builds the board.
package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/config"
	"github.com/nono/rook/internal/github"
)

// Fetcher reads the open PRs of a repo.
type Fetcher interface {
	OpenPRs(ctx context.Context, repo string) ([]github.PR, error)
}

// Load reads the PRs of all the repos in parallel and builds the board.
// A repo that fails gives a RepoError; the other repos still show.
func Load(ctx context.Context, f Fetcher, me string, cfg config.Config, now func() time.Time) board.Board {
	prs := make([][]github.PR, len(cfg.Repos))
	errs := make([]error, len(cfg.Repos))
	var wg sync.WaitGroup
	for i, repo := range cfg.Repos {
		wg.Go(func() { prs[i], errs[i] = f.OpenPRs(ctx, repo) })
	}
	wg.Wait()

	var all []github.PR
	var repoErrs []board.RepoError
	for i, repo := range cfg.Repos {
		if errs[i] != nil {
			repoErrs = append(repoErrs, board.RepoError{Repo: repo, Err: strings.TrimPrefix(errs[i].Error(), repo+": ")})
			continue
		}
		all = append(all, prs[i]...)
	}
	opts := board.Options{
		Me:                me,
		ShortcutWorkspace: cfg.ShortcutWorkspace,
		Ignore:            cfg.IgnoreReviewers,
		ShowRepo:          len(cfg.Repos) > 1,
	}
	return board.Board{Sections: board.Build(all, opts), Errors: repoErrs, FetchedAt: now()}
}
