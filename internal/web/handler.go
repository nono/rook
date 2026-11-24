// Package web renders the dashboard page.
package web

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/nono/rook/internal/board"
	"github.com/nono/rook/internal/loopback"
)

//go:embed page.html
var pageHTML string

// Source gives the board to show. It is called at each page load.
type Source func(ctx context.Context) board.Board

type groupStyle struct{ Name, Hint, Color string }

var groupStyles = map[board.Group]groupStyle{
	board.Review:   {"Review", "your turn to review", "#ea580c"},
	board.WIP:      {"WIP", "your turn to fix", "#ca8a04"},
	board.Conflict: {"Conflict", "rebase needed", "#dc2626"},
	board.Waiting:  {"Waiting", "others' turn", "#2563eb"},
	board.Team:     {"Team", "FYI", "#6b7280"},
}

// labelTones gives the CSS class of a label; the other labels are gray.
var labelTones = map[string]string{"CI red": "red", "conflict": "red", "CI pending": "ylw", "no reviewer": "ylw", "Claude": "ylw"}

var stateWords = map[board.ReviewerState]string{
	board.Approved:  "approved",
	board.Changes:   "requested changes",
	board.Commented: "commented",
	board.Dismissed: "review dismissed",
	board.Requested: "review requested",
}

// shortAge gives a short form of d: "5m", "3h", "2d". A negative d (a local
// clock behind GitHub's) gives "0m".
func shortAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(max(d, 0)/time.Minute))
}

// loopbackHost tells if the Host header is "localhost" or a loopback IP. Rook
// refuses all other names, to block DNS rebinding: a foreign page that
// resolves its own domain to 127.0.0.1.
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return loopback.Host(host)
}

// NewHandler serves the page at "/" and the SVG icon at "/favicon.svg", to
// loopback hosts only.
func NewHandler(src Source, now func() time.Time, icon []byte) http.Handler {
	tmpl := template.Must(template.New("page").Funcs(template.FuncMap{
		"age":       func(t time.Time) string { return shortAge(now().Sub(t)) },
		"group":     func(g board.Group) groupStyle { return groupStyles[g] },
		"tone":      func(label string) string { return cmp.Or(labelTones[label], "gry") },
		"stateWord": func(s board.ReviewerState) string { return stateWords[s] },
	}).Parse(pageHTML))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "rook serves only loopback hosts", http.StatusMisdirectedRequest)
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/favicon.svg" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/favicon.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write(icon)
			return
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, src(r.Context())); err != nil {
			log.Printf("render: %v", err)
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store") // each load must read fresh data
		_, _ = w.Write(buf.Bytes())
	})
}
