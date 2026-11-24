// Package web renders the dashboard page.
package web

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/nono/rook/internal/board"
)

//go:embed page.html
var pageHTML string

// Source gives the board to show. It is called at each page load.
type Source func(ctx context.Context) board.Board

var ciIcons = map[board.CI]string{board.CIGreen: "●", board.CIRed: "●", board.CIPending: "●", board.CINone: "○"}

var stateWords = map[board.ReviewerState]string{
	board.Approved:  "approved",
	board.Changes:   "requested changes",
	board.Commented: "commented",
	board.Requested: "review requested",
}

// Age gives a short form of d: "5m", "3h", "2d".
func Age(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// NewHandler serves the page at "/".
func NewHandler(src Source, now func() time.Time) http.Handler {
	tmpl := template.Must(template.New("page").Funcs(template.FuncMap{
		"age":       func(t time.Time) string { return Age(now().Sub(t)) },
		"ciIcon":    func(c board.CI) string { return ciIcons[c] },
		"stateWord": func(s board.ReviewerState) string { return stateWords[s] },
	}).Parse(pageHTML))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, src(r.Context())); err != nil {
			log.Printf("render: %v", err)
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	})
}
