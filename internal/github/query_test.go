package github

import (
	"regexp"
	"strings"
	"testing"
)

// The fake server answers any query text, so a field lost from the query
// leaves the fixtures full and the suite green, while GitHub sends nothing
// for it. This test pins each field that pr() maps, at its path.
func TestQueryAsksForMappedFields(t *testing.T) {
	got := queryPaths(t, prQuery)
	want := []string{
		"repository.pullRequests.pageInfo.hasNextPage",
		"repository.pullRequests.pageInfo.endCursor",
		"repository.pullRequests.nodes.statusCheckRollup.state",
		"repository.pullRequests.nodes.reviewRequests.nodes.requestedReviewer.__typename",
		"repository.pullRequests.nodes.reviewRequests.nodes.requestedReviewer.User.login",
		"repository.pullRequests.nodes.reviewRequests.nodes.requestedReviewer.User.avatarUrl",
		"repository.pullRequests.nodes.reviewRequests.nodes.requestedReviewer.Team.slug",
		"repository.pullRequests.nodes.reviewRequests.nodes.requestedReviewer.EnterpriseTeam.slug",
	}
	for _, f := range []string{"number", "title", "url", "isDraft", "updatedAt", "headRefName", "headRefOid", "mergeable",
		"author.__typename", "author.login", "author.avatarUrl"} {
		want = append(want, "repository.pullRequests.nodes."+f)
	}
	for _, list := range []string{"reviews", "mine"} {
		for _, f := range []string{"id", "state", "submittedAt", "commit.oid", "author.__typename", "author.login", "author.avatarUrl"} {
			want = append(want, "repository.pullRequests.nodes."+list+".nodes."+f)
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("query does not ask for %s", w)
		}
	}
}

func TestQueryPaths(t *testing.T) {
	got := queryPaths(t, `query($a: String) { r(x: $a, y: [1, 2]) { b c: d(n: 1) { e } g ... on T { f } } }`)
	for _, w := range []string{"r.b", "r.c.e", "r.g", "r.T.f"} {
		if !got[w] {
			t.Errorf("no path %s in %v", w, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("paths = %v, want 4", got)
	}
}

// queryPaths gives the dotted path of each leaf field of a GraphQL query.
// An alias names its field, an inline fragment adds its type name, and
// arguments are skipped. It knows only the syntax that query.graphql uses.
func queryPaths(t *testing.T, q string) map[string]bool {
	t.Helper()
	tokens := regexp.MustCompile(`\.\.\.|[A-Za-z_][A-Za-z0-9_]*|[{}():]|"[^"]*"|\S`).FindAllString(q, -1)
	paths := map[string]bool{}
	var stack []string
	last := ""
	leaf := func() {
		if last != "" {
			paths[strings.Join(append(stack[1:len(stack):len(stack)], last), ".")] = true
		}
	}
	for i := 0; i < len(tokens); i++ {
		switch tok := tokens[i]; tok {
		case "(":
			for depth := 1; depth > 0; {
				i++
				switch tokens[i] {
				case "(":
					depth++
				case ")":
					depth--
				}
			}
		case "{":
			stack = append(stack, last)
			last = ""
		case "}":
			leaf()
			stack = stack[:len(stack)-1]
			last = ""
		case "...":
			leaf()
			i += 2 // "on" and the type name
			last = tokens[i]
		default:
			leaf()
			last = tok
			if i+1 < len(tokens) && tokens[i+1] == ":" {
				i += 2 // alias: keep the alias, skip the field name
			}
		}
	}
	if len(stack) != 0 {
		t.Fatalf("unbalanced query: %v", stack)
	}
	return paths
}
