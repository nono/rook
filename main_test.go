package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
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
