package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
