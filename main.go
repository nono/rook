// Command rook serves a dashboard of the open PRs of some GitHub repos.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

// loadTimeout limits the GitHub calls of one page load. A repo that is slower
// gets an error banner, and the page shows the other repos.
const loadTimeout = 8 * time.Second

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
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	log.Printf("rook: http://%s/ (user %s)", ln.Addr(), me)
	return serveHTTP(ctx, ln, web.NewHandler(src, time.Now))
}

// serveHTTP serves h on ln until ctx ends. The end of ctx also cancels the
// page loads in progress, so that the stop does not wait for GitHub.
func serveHTTP(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func ghToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
	token := strings.TrimSpace(string(out))
	if err != nil || token == "" {
		return "", fmt.Errorf(`no GitHub token: run "gh auth login" first (%v)`, err)
	}
	return token, nil
}
