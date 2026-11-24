package github

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is the GitHub GraphQL API.
const DefaultEndpoint = "https://api.github.com/graphql"

//go:embed query.graphql
var prQuery string

// Client calls the GitHub GraphQL API.
type Client struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
	// Me is the login of the user. OpenPRs also reads the last 100 reviews
	// of Me, which the last 100 reviews of the PR can miss.
	Me string
}

// Viewer gives the login of the owner of the token.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	var out struct {
		Viewer struct{ Login string }
	}
	if err := c.post(ctx, "query { viewer { login } }", nil, &out); err != nil {
		return "", fmt.Errorf("viewer: %w", err)
	}
	return out.Viewer.Login, nil
}

type gqlActor struct {
	Typename  string `json:"__typename"`
	Login     string
	AvatarURL string `json:"avatarUrl"`
}

// actor maps a GraphQL actor. Only a User is a person: a Bot, a Mannequin and
// a deleted account (null, shown as "ghost") get Bot set.
func (a *gqlActor) actor() Actor {
	if a == nil {
		return Actor{Login: "ghost", Bot: true}
	}
	return Actor{Login: a.Login, AvatarURL: a.AvatarURL, Bot: a.Typename != "User"}
}

type gqlReview struct {
	ID          string
	State       string
	SubmittedAt time.Time
	Commit      *struct{ Oid string }
	Author      *gqlActor
}

type gqlRollup *struct{ State string }

type gqlPR struct {
	Number      int
	Title       string
	URL         string
	IsDraft     bool
	UpdatedAt   time.Time
	HeadRefName string
	HeadRefOid  string
	Mergeable   string
	Author      *gqlActor
	Requests    struct {
		Nodes []struct {
			RequestedReviewer *struct {
				gqlActor
				Slug string
			}
		}
	} `json:"reviewRequests"`
	Reviews           struct{ Nodes []gqlReview }
	Mine              struct{ Nodes []gqlReview }
	StatusCheckRollup gqlRollup
}

func (g gqlPR) pr(repo string) PR {
	p := PR{
		Repo: repo, Number: g.Number, Title: g.Title, URL: g.URL, IsDraft: g.IsDraft,
		UpdatedAt: g.UpdatedAt, HeadRefName: g.HeadRefName, HeadOID: g.HeadRefOid,
		Mergeable: g.Mergeable, Author: g.Author.actor(),
	}
	for _, n := range g.Requests.Nodes {
		r := n.RequestedReviewer
		switch {
		case r == nil:
		case r.Typename == "Team" || r.Typename == "EnterpriseTeam":
			p.RequestedTeams = append(p.RequestedTeams, r.Slug)
		case r.Typename == "User":
			p.RequestedUsers = append(p.RequestedUsers, r.actor())
		}
	}
	// Mine holds my last 100 reviews: the last 100 reviews of the PR can miss them.
	seen := map[string]bool{}
	for _, n := range append(g.Reviews.Nodes, g.Mine.Nodes...) {
		if n.State == "PENDING" || (n.ID != "" && seen[n.ID]) {
			continue
		}
		seen[n.ID] = true
		r := Review{Author: n.Author.actor(), State: n.State, SubmittedAt: n.SubmittedAt}
		if n.Commit != nil {
			r.CommitOID = n.Commit.Oid
		}
		p.Reviews = append(p.Reviews, r)
	}
	// GitHub gives the PR rollup for the head ref, with no cap on the number
	// of commits. It matched the rollup of the last commit on 134 open fork
	// PRs of two public repos.
	if g.StatusCheckRollup != nil {
		p.CIState = g.StatusCheckRollup.State
	}
	return p
}

// OpenPRs gives all the open PRs of repo ("owner/name").
func (c *Client) OpenPRs(ctx context.Context, repo string) ([]PR, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repo %q: want \"owner/name\"", repo)
	}
	var prs []PR
	var cursor *string
	for {
		var out struct {
			Repository *struct {
				PullRequests struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []gqlPR
				}
			}
		}
		vars := map[string]any{"owner": owner, "name": name, "cursor": cursor, "me": c.Me}
		if err := c.post(ctx, prQuery, vars, &out); err != nil {
			return nil, fmt.Errorf("%s: %w", repo, err)
		}
		if out.Repository == nil {
			return nil, fmt.Errorf("%s: repository not found", repo)
		}
		page := out.Repository.PullRequests
		for _, n := range page.Nodes {
			prs = append(prs, n.pr(repo))
		}
		if !page.PageInfo.HasNextPage {
			return prs, nil
		}
		next := page.PageInfo.EndCursor
		cursor = &next
	}
}

func (c *Client) post(ctx context.Context, query string, vars map[string]any, data any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(out.Errors) > 0 {
		return errors.New(out.Errors[0].Message)
	}
	return json.Unmarshal(out.Data, data)
}
