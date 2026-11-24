package web

import (
	"context"
	"html"
	"html/template"
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

var testIcon = []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)

func TestShortAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:             "0m",
		5 * time.Minute:              "5m",
		3*time.Hour + 10*time.Minute: "3h",
		49 * time.Hour:               "2d",
		-5 * time.Minute:             "0m",
		-3 * time.Hour:               "0m",
	} {
		if got := shortAge(d); got != want {
			t.Errorf("shortAge(%s) = %q, want %q", d, got, want)
		}
	}
}

func sampleBoard(title string) board.Board {
	secs := board.Build(nil, board.Options{})
	secs[0].Cards = []board.Card{{
		Kind: "pr", Repo: "o/r", Number: 1, Title: title, URL: "https://github.com/o/r/pull/1",
		StoryID: "9", StoryURL: "https://app.shortcut.com/ws/story/9",
		Labels: []string{"no reviewer", "draft"},
		CI:     board.CINone, Author: github.Actor{Login: "alice", AvatarURL: "https://a/alice"},
		Reviewers: []board.Reviewer{
			{Login: "bob", AvatarURL: "https://a/bob", State: board.Approved},
			{Login: "carl", AvatarURL: "https://a/carl", State: board.Changes},
			{Login: "dave", AvatarURL: "https://a/dave", State: board.Dismissed},
			{Login: "core", Team: true, State: board.Requested},
		},
		UpdatedAt: now.Add(-2 * time.Hour),
	}}
	return board.Board{Sections: secs, Errors: []board.RepoError{{Repo: "o/x", Err: "boom"}}, FetchedAt: now}
}

func get(t *testing.T, b board.Board, method, path string) (int, string) {
	t.Helper()
	h := NewHandler(func(context.Context) board.Board { return b }, func() time.Time { return now }, testIcon)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1:7878"
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestPageOrderAndContent(t *testing.T) {
	b := sampleBoard("Do it")
	code, body := get(t, b, http.MethodGet, "/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	last := -1
	for _, sec := range b.Sections {
		g := sec.Group
		i := strings.Index(body, `id="g-`+string(g)+`"`)
		if i < 0 || i < last {
			t.Fatalf("section %s at %d, previous at %d", g, i, last)
		}
		last = i
	}
	if strings.Contains(body, `class="repo"`) {
		t.Error("one repo: the card must not show the repo name")
	}
	for _, want := range []string{
		`href="https://app.shortcut.com/ws/story/9"`,
		`<a class="t card-link" href="https://github.com/o/r/pull/1"`,
		"nothing", "o/x: boom", "Refresh", "your turn to review",
		`<span class="lab ylw">no reviewer</span>`, `<span class="lab gry">draft</span>`, "Do it",
		`<span class="mk ok">✓</span>`, `<span class="mk ko">✗</span>`, `title="CI none">○<`, "carl: requested changes",
		"updated 2h ago", "alice", "https://a/bob", "bob: approved", "core",
		"background:#ea580c",
		`<link rel="icon" href="/favicon.svg" type="image/svg+xml">`,
		"Data from " + now.Local().Format("2006-01-02 15:04:05"),
		"dave: review dismissed",
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
	b.ShowRepo = true
	_, body := get(t, b, http.MethodGet, "/")
	if !strings.Contains(body, "mine") || !strings.Contains(body, `<div class="repo">o/r</div>`) {
		t.Fatal("mine card should say mine and show the repo")
	}
}

func TestPageEscapesTitle(t *testing.T) {
	_, body := get(t, sampleBoard("<script>alert(1)</script>"), http.MethodGet, "/")
	if strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("title is not escaped")
	}
}

// DNS rebinding: a page on another domain that resolves to 127.0.0.1 must not read the board.
func TestRejectsForeignHost(t *testing.T) {
	h := NewHandler(func(context.Context) board.Board { return sampleBoard("x") }, func() time.Time { return now }, testIcon)
	for host, want := range map[string]int{
		"evil.example:7878": http.StatusMisdirectedRequest,
		"evil.example":      http.StatusMisdirectedRequest,
		"127.0.0.1:7878":    http.StatusOK,
		"localhost:7878":    http.StatusOK,
		"[::1]:7878":        http.StatusOK,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, rec.Code, want)
		}
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

func TestPageIsNotCached(t *testing.T) {
	h := NewHandler(func(context.Context) board.Board { return sampleBoard("x") }, func() time.Time { return now }, testIcon)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:7878"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// A card with many reviewers must wrap its avatars, not hide them.
func TestAvatarsWrap(t *testing.T) {
	i := strings.Index(pageHTML, ".avs {")
	if i < 0 {
		t.Fatal("no .avs rule")
	}
	rule := pageHTML[i : i+strings.Index(pageHTML[i:], "}")]
	if !strings.Contains(rule, "flex-wrap: wrap") {
		t.Fatalf(".avs rule = %q, want flex-wrap: wrap", rule)
	}
}

// The title link covers the whole card, so a click anywhere opens the PR.
func TestCardLinkCoversCard(t *testing.T) {
	for _, want := range []string{
		".card { position: relative;",
		`.card a.card-link::after { content: ""; position: absolute; inset: 0; }`,
	} {
		if !strings.Contains(pageHTML, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

func TestHead(t *testing.T) {
	if code, _ := get(t, sampleBoard("x"), http.MethodHead, "/"); code != http.StatusOK {
		t.Fatalf("HEAD / status = %d, want 200", code)
	}
}

func TestEveryGroupHasStyle(t *testing.T) {
	for _, s := range board.Build(nil, board.Options{}) {
		if st := groupStyles[s.Group]; st.Name == "" || st.Hint == "" || st.Color == "" {
			t.Errorf("group %s has no style", s.Group)
		}
	}
	want := map[board.Group]groupStyle{
		board.Review:   {"Review", "your turn to review", "#ea580c"},
		board.WIP:      {"WIP", "your turn to fix", "#ca8a04"},
		board.Conflict: {"Conflict", "rebase needed", "#dc2626"},
		board.Waiting:  {"Waiting", "others' turn", "#2563eb"},
		board.Team:     {"Team", "FYI", "#6b7280"},
	}
	for g, w := range want {
		if groupStyles[g] != w {
			t.Errorf("style of %s = %+v, want %+v", g, groupStyles[g], w)
		}
	}
}

func TestLabelTones(t *testing.T) {
	for label, want := range map[string]string{
		"CI red": "red", "conflict": "red", "CI pending": "ylw", "no reviewer": "ylw", "Claude": "ylw",
		"not reviewed": "gry", "requested again": "gry", "new commits": "gry", "draft": "gry", "merge ?": "gry",
	} {
		b := sampleBoard("x")
		b.Sections[0].Cards[0].Labels = []string{label}
		_, body := get(t, b, http.MethodGet, "/")
		if !strings.Contains(body, `<span class="lab `+want+`">`+template.HTMLEscapeString(label)+`</span>`) {
			t.Errorf("label %q: want tone %s", label, want)
		}
	}
}

func TestFavicon(t *testing.T) {
	h := NewHandler(func(context.Context) board.Board { return sampleBoard("x") }, func() time.Time { return now }, testIcon)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/favicon.svg", nil)
		req.Host = "127.0.0.1:7878"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
			t.Fatalf("%s /favicon.svg: status %d, type %q", method, rec.Code, rec.Header().Get("Content-Type"))
		}
		if method == http.MethodGet && rec.Body.String() != string(testIcon) {
			t.Fatalf("body = %q", rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	req.Host = "evil.example:7878"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host: status %d", rec.Code)
	}
}

func TestClaudeLogo(t *testing.T) {
	b := sampleBoard("Do it")
	second := b.Sections[0].Cards[0]
	second.Number = 2
	b.Sections[0].Cards[0].SessionDirs = []string{"/w/1", "/w/2"}
	b.Sections[0].Cards = append(b.Sections[0].Cards, second)
	_, body := get(t, b, http.MethodGet, "/")
	if n := strings.Count(body, `class="claude"`); n != 1 {
		t.Fatalf("%d Claude logos, want 1", n)
	}
	i := strings.Index(body, `class="claude"`)
	if ci := strings.Index(body, `class="ci `); ci < 0 || i < ci {
		t.Errorf("logo at %d, before the CI dot at %d", i, ci)
	}
	tag, _, _ := strings.Cut(body[i:], ">")
	tag = html.UnescapeString(tag)
	if want := `title="Claude Code in /w/1` + "\n" + `Claude Code in /w/2"`; !strings.Contains(tag, want) {
		t.Errorf("logo tag %q does not contain %q", tag, want)
	}
}

// The logo shows in each group, not only in Review.
func TestClaudeLogoInEachGroup(t *testing.T) {
	b := sampleBoard("Do it")
	card := b.Sections[0].Cards[0]
	card.SessionDirs = []string{"/w"}
	for i := range b.Sections {
		b.Sections[i].Cards = []board.Card{card}
	}
	_, body := get(t, b, http.MethodGet, "/")
	for _, sec := range b.Sections {
		start := strings.Index(body, `id="g-`+string(sec.Group)+`"`)
		rest := body[start+1:]
		if end := strings.Index(rest, `id="g-`); end >= 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, `class="claude"`) {
			t.Errorf("group %s: no Claude logo", sec.Group)
		}
	}
}
