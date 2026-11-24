package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/github"
)

var now = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:             "0m",
		5 * time.Minute:              "5m",
		3*time.Hour + 10*time.Minute: "3h",
		49 * time.Hour:               "2d",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%s) = %q, want %q", d, got, want)
		}
	}
}

func sampleBoard(title string) board.Board {
	secs := board.Build(nil, board.Options{})
	secs[0].Cards = []board.Card{{
		Kind: "pr", Repo: "o/r", Number: 1, Title: title, URL: "https://github.com/o/r/pull/1",
		StoryID: "9", StoryURL: "https://app.shortcut.com/ws/story/9", Group: board.Review,
		Labels: []board.Label{{Text: "not reviewed", Tone: "gry"}, {Text: "draft", Tone: "gry"}},
		CI:     board.CIGreen, Author: github.Actor{Login: "alice", AvatarURL: "https://a/alice"},
		Reviewers: []board.Reviewer{
			{Login: "bob", AvatarURL: "https://a/bob", State: board.Approved},
			{Login: "core", Team: true, State: board.Requested},
		},
		UpdatedAt: now.Add(-2 * time.Hour),
	}}
	return board.Board{Sections: secs, Errors: []board.RepoError{{Repo: "o/x", Err: "boom"}}, FetchedAt: now}
}

func get(t *testing.T, b board.Board, method, path string) (int, string) {
	t.Helper()
	h := NewHandler(func(context.Context) board.Board { return b }, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestPageOrderAndContent(t *testing.T) {
	code, body := get(t, sampleBoard("Do it"), http.MethodGet, "/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	last := -1
	for _, g := range board.Order {
		i := strings.Index(body, `id="g-`+string(g)+`"`)
		if i < 0 || i < last {
			t.Fatalf("section %s at %d, previous at %d", g, i, last)
		}
		last = i
	}
	for _, want := range []string{
		`href="https://app.shortcut.com/ws/story/9"`,
		`data-url="https://github.com/o/r/pull/1"`,
		"nothing", "o/x: boom", "Refresh", "your turn to review",
		"not reviewed", "draft", "stopPropagation", "Do it",
		"updated 2h ago", "alice", "https://a/bob", "bob: approved", "core",
		"background:#ea580c",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestPageStoryWithoutURL(t *testing.T) {
	b := sampleBoard("Do it")
	b.Sections[0].Cards[0].StoryURL = ""
	_, body := get(t, b, http.MethodGet, "/")
	if !strings.Contains(body, "sc-9") || strings.Contains(body, "app.shortcut.com") {
		t.Fatal("story without URL should show as plain text")
	}
}

func TestPageMine(t *testing.T) {
	b := sampleBoard("Do it")
	b.Sections[0].Cards[0].Mine = true
	b.Sections[0].Cards[0].ShowRepo = true
	_, body := get(t, b, http.MethodGet, "/")
	if !strings.Contains(body, "mine") || !strings.Contains(body, "o/r") {
		t.Fatal("mine card should say mine and show the repo")
	}
}

func TestPageEscapesTitle(t *testing.T) {
	_, body := get(t, sampleBoard("<script>alert(1)</script>"), http.MethodGet, "/")
	if strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("title is not escaped")
	}
}

func TestNotFound(t *testing.T) {
	if code, _ := get(t, sampleBoard("x"), http.MethodGet, "/x"); code != http.StatusNotFound {
		t.Fatalf("status = %d", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	if code, _ := get(t, sampleBoard("x"), http.MethodPost, "/"); code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", code)
	}
}
