package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/config"
	"github.com/nono/rook/internal/github"
)

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"serve", "--bad"}, {"serve", "extra"}} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, &stderr); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage: rook serve") {
			t.Errorf("run(%q) stderr = %q", args, stderr.String())
		}
	}
}

func TestRunMissingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none.toml")
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"serve", "--config", path}, &stderr); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), path) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// gh auth token follows GH_HOST; rook calls api.github.com, so it must ask for that host.
func TestGhTokenAsksForGitHubCom(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"token-for $*\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := ghToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-for auth token --hostname github.com" {
		t.Fatalf("ghToken() = %q", got)
	}
}

// The spec asks for a page in a few seconds: one stalled repo must not hold it longer.
func TestLoadTimeoutIsAFewSeconds(t *testing.T) {
	if loadTimeout > 10*time.Second {
		t.Fatalf("loadTimeout = %s, want 10s or less", loadTimeout)
	}
}

// A stop during a slow page load must cancel the load and end without an error.
func TestServeHTTPStopsDuringLoad(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, h) }()
	go func() { _, _ = http.Get("http://" + ln.Addr().String() + "/") }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP() = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTP did not stop")
	}
}

func TestIconIsRookSVG(t *testing.T) {
	want, err := os.ReadFile("rook.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, want) || !bytes.HasPrefix(icon, []byte("<svg")) {
		t.Fatalf("icon is not rook.svg (%d bytes)", len(icon))
	}
}

type fetcher []github.PR

func (f fetcher) OpenPRs(context.Context, string) ([]github.PR, error) { return f, nil }

// The page source finds the sessions in the proc root and gives them to the
// board: a claude process in a checkout of branch x moves my PR to WIP.
func TestNewSourceFindsSessions(t *testing.T) {
	// Run from a git hook, the environment names the repo of the hook: git
	// must not use it.
	vars, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range strings.Fields(string(vars)) {
		t.Setenv(v, "")
		if err := os.Unsetenv(v); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "x"}, {"remote", "add", "origin", "git@github.com:o/a.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	proc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proc, "10"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "10", "comm"), []byte("claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(proc, "10", "cwd")); err != nil {
		t.Fatal(err)
	}
	p := github.PR{Repo: "o/a", Number: 1, HeadRefName: "x", Author: github.Actor{Login: "me"}, CIState: "SUCCESS", Mergeable: "MERGEABLE",
		RequestedUsers: []github.Actor{{Login: "alice"}}}
	b := newSource(fetcher{p}, proc, t.Logf, "me", config.Config{Repos: []string{"o/a"}})(context.Background())
	if wip := b.Sections[1]; wip.Group != board.WIP || len(wip.Cards) != 1 || !slices.Equal(wip.Cards[0].SessionDirs, []string{dir}) {
		t.Fatalf("WIP = %+v", wip)
	}
}

func TestSessionSource(t *testing.T) {
	var logs []string
	logf := func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) }
	if s := sessionSource(t.TempDir(), logf); s == nil || len(logs) != 0 {
		t.Fatalf("readable proc: sessions nil = %v, logs = %q", s == nil, logs)
	}
	if s := sessionSource(filepath.Join(t.TempDir(), "none"), logf); s != nil || len(logs) != 1 || !strings.Contains(logs[0], "no Claude Code sessions") {
		t.Fatalf("missing proc: sessions nil = %v, logs = %q", s == nil, logs)
	}
	logs = nil
	t.Setenv("PATH", t.TempDir())
	if s := sessionSource(t.TempDir(), logf); s != nil || len(logs) != 1 || !strings.Contains(logs[0], "git") {
		t.Fatalf("no git: sessions nil = %v, logs = %q", s == nil, logs)
	}
}
