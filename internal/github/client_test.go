package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// server answers each request with handle(req); it fails the test on a bad auth header or method.
func server(t *testing.T, handle func(req gqlRequest) (int, string)) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		code, body := handle(req)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{Endpoint: srv.URL, Token: "tok", HTTP: srv.Client(), Me: "me"}, &calls
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func pages(t *testing.T) (*Client, *atomic.Int32) {
	return server(t, func(req gqlRequest) (int, string) {
		if req.Variables["owner"] != "o" || req.Variables["name"] != "r" || req.Variables["me"] != "me" {
			t.Errorf("variables = %v", req.Variables)
		}
		switch req.Variables["cursor"] {
		case nil:
			return 200, fixture(t, "page1.json")
		case "c1":
			return 200, fixture(t, "page2.json")
		}
		t.Errorf("unexpected cursor %v", req.Variables["cursor"])
		return 500, ""
	})
}

func TestViewer(t *testing.T) {
	c, _ := server(t, func(gqlRequest) (int, string) { return 200, fixture(t, "viewer.json") })
	got, err := c.Viewer(context.Background())
	if err != nil || got != "me" {
		t.Fatalf("Viewer() = %q, %v", got, err)
	}
}

func TestOpenPRsFollowsPages(t *testing.T) {
	c, calls := pages(t)
	prs, err := c.OpenPRs(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	var nums []int
	for _, p := range prs {
		nums = append(nums, p.Number)
	}
	if !reflect.DeepEqual(nums, []int{11, 12, 13, 14}) {
		t.Fatalf("numbers = %v", nums)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestOpenPRsMapsFields(t *testing.T) {
	c, _ := pages(t)
	prs, err := c.OpenPRs(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	want := PR{
		Repo: "o/r", CurrentRepo: "o/r", Number: 11, Title: "[sc-1] First", URL: "https://github.com/o/r/pull/11",
		UpdatedAt:   time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC),
		HeadRefName: "me/sc-1-first", HeadOID: "h11", Mergeable: "MERGEABLE",
		Author:         Actor{Login: "me", AvatarURL: "https://a/me"},
		RequestedUsers: []Actor{{Login: "alice", AvatarURL: "https://a/alice"}},
		RequestedTeams: []string{"core", "ent"},
		Reviews: []Review{
			{Author: Actor{Login: "rabbit", AvatarURL: "https://a/rabbit", Bot: true}, State: "COMMENTED",
				SubmittedAt: time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC), CommitOID: "h10"},
			{Author: Actor{Login: "carol", AvatarURL: "https://a/carol"}, State: "APPROVED",
				SubmittedAt: time.Date(2026, 11, 24, 7, 30, 0, 0, time.UTC)},
			{Author: Actor{Login: "old", AvatarURL: "https://a/old", Bot: true}, State: "COMMENTED",
				SubmittedAt: time.Date(2026, 11, 24, 7, 40, 0, 0, time.UTC), CommitOID: "h11"},
			{Author: Actor{Login: "me", AvatarURL: "https://a/me"}, State: "CHANGES_REQUESTED",
				SubmittedAt: time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC), CommitOID: "h9"},
		},
		CIState:           "FAILURE",
		CodeRabbitThreads: 2,
	}
	if !reflect.DeepEqual(prs[0], want) {
		t.Fatalf("got  %+v\nwant %+v", prs[0], want)
	}
	if prs[1].CIState != "" || !prs[1].IsDraft || prs[1].Mergeable != "UNKNOWN" || !prs[1].Fork || prs[1].CodeRabbitThreads != 0 {
		t.Errorf("PR 12 = %+v", prs[1])
	}
	if prs[2].CIState != "PENDING" || prs[2].Mergeable != "CONFLICTING" {
		t.Errorf("PR 13 = %+v", prs[2])
	}
	if prs[3].CIState != "" {
		t.Errorf("PR 14 (no checks) = %+v", prs[3])
	}
}

func TestOpenPRsNullAuthor(t *testing.T) {
	c, _ := pages(t)
	prs, err := c.OpenPRs(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if a, r := prs[1].Author, prs[1].Reviews[0].Author; a.Login != "ghost" || !a.Bot || r.Login != "ghost" || !r.Bot {
		t.Fatalf("PR 12 = %+v", prs[1])
	}
}

// GitHub follows a rename: the PRs of a renamed repo keep the configured name,
// and carry the current one, which the remote URL of a new clone has.
func TestOpenPRsRenamedRepo(t *testing.T) {
	c, _ := server(t, func(gqlRequest) (int, string) {
		return 200, `{"data":{"repository":{"nameWithOwner":"o/new","pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":1}]}}}}`
	})
	prs, err := c.OpenPRs(context.Background(), "o/old")
	if err != nil || len(prs) != 1 || prs[0].Repo != "o/old" || prs[0].CurrentRepo != "o/new" {
		t.Fatalf("prs = %+v, err = %v", prs, err)
	}
}

func TestOpenPRsGraphQLError(t *testing.T) {
	c, _ := server(t, func(gqlRequest) (int, string) {
		return 200, `{"data":{"repository":null},"errors":[{"message":"Could not resolve to a Repository"}]}`
	})
	_, err := c.OpenPRs(context.Background(), "o/r")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve to a Repository") || !strings.Contains(err.Error(), "o/r") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenPRsHTTPError(t *testing.T) {
	c, _ := server(t, func(gqlRequest) (int, string) { return 401, `{"message":"Bad credentials"}` })
	_, err := c.OpenPRs(context.Background(), "o/r")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenPRsBadRepo(t *testing.T) {
	c, calls := server(t, func(gqlRequest) (int, string) { return 200, "{}" })
	if _, err := c.OpenPRs(context.Background(), "norepo"); err == nil {
		t.Fatal("no error")
	}
	if calls.Load() != 0 {
		t.Fatalf("calls = %d, want 0", calls.Load())
	}
}
