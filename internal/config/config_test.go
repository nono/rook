package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustFail(t *testing.T, content, want string) {
	t.Helper()
	_, err := Load(write(t, content))
	if err == nil {
		t.Fatalf("Load(%q): no error, want one with %q", content, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Load(%q): error %q does not contain %q", content, err, want)
	}
}

func TestLoadFull(t *testing.T) {
	cfg, err := Load(write(t, `
listen = "127.0.0.1:9000"
repos = ["o/a", "o/b"]
shortcut_workspace = "ws"
ignore_reviewers = ["rabbit"]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Listen:            "127.0.0.1:9000",
		Repos:             []string{"o/a", "o/b"},
		ShortcutWorkspace: "ws",
		IgnoreReviewers:   []string{"rabbit"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadDefaultListen(t *testing.T) {
	cfg, err := Load(write(t, `repos = ["a/b"]`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7878" {
		t.Fatalf("Listen = %q, want 127.0.0.1:7878", cfg.Listen)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	mustFail(t, "repos = [\"a/b\"]\nrepo = [\"a/b\"]", "unknown keys: repo")
}

func TestLoadRejectsDuplicateRepo(t *testing.T) {
	mustFail(t, `repos = ["o/a", "O/A"]`, `"O/A"`)
}

func TestLoadRejectsNoRepos(t *testing.T) {
	mustFail(t, ``, "repos")
}

func TestLoadRejectsBadRepo(t *testing.T) {
	for _, bad := range []string{"a", "a/", "/b", "a/b/c"} {
		mustFail(t, `repos = ["`+bad+`"]`, `"`+bad+`"`)
	}
}

func TestLoadRejectsNonLoopback(t *testing.T) {
	mustFail(t, "repos = [\"a/b\"]\nlisten = \"0.0.0.0:7878\"", "loopback")
	mustFail(t, "repos = [\"a/b\"]\nlisten = \":7878\"", "loopback")
	for _, ok := range []string{"localhost:1", "127.0.0.1:1", "[::1]:1"} {
		if _, err := Load(write(t, "repos = [\"a/b\"]\nlisten = \""+ok+"\"")); err != nil {
			t.Errorf("listen %q: %v", ok, err)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.toml")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one with %q", err, path)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/x/rook/config.toml" {
		t.Fatalf("DefaultPath() = %q", got)
	}
}
