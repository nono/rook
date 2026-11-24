// Command rook serves a dashboard of the open PRs of some GitHub repos.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nono/rook/internal/app"
	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/config"
	"github.com/nono/rook/internal/github"
	"github.com/nono/rook/internal/web"
)

const usage = "usage: rook serve [--config PATH]"

// loadTimeout limits the GitHub calls of one page load.
const loadTimeout = 20 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "serve" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path of the config file (default: the user config directory + rook/config.toml)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if err := serve(ctx, *cfgPath); err != nil {
		_, _ = fmt.Fprintln(stderr, "rook:", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfgPath string) error {
	if cfgPath == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return err
		}
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	token, err := ghToken(ctx)
	if err != nil {
		return err
	}
	client := &github.Client{Endpoint: github.DefaultEndpoint, Token: token, HTTP: &http.Client{Timeout: loadTimeout}}
	me, err := client.Viewer(ctx)
	if err != nil {
		return err
	}
	client.Me = me
	src := func(ctx context.Context) board.Board {
		ctx, cancel := context.WithTimeout(ctx, loadTimeout)
		defer cancel()
		return app.Load(ctx, client, me, cfg, time.Now)
	}
	srv := &http.Server{Addr: cfg.Listen, Handler: web.NewHandler(src, time.Now), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("rook: http://%s/ (user %s)", cfg.Listen, me)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func ghToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	token := strings.TrimSpace(string(out))
	if err != nil || token == "" {
		return "", fmt.Errorf(`no GitHub token: run "gh auth login" first (%v)`, err)
	}
	return token, nil
}
