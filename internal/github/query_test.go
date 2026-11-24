package github

import (
	"maps"
	"regexp"
	"strings"
	"testing"
)

// The fake server answers any query text, so a field or an argument lost
// from the query leaves the fixtures full and the suite green, while GitHub
// sends less. These tests pin each field that pr() maps, at its path, and
// the arguments of each field.
func TestQueryAsksForMappedFields(t *testing.T) {
	got, _ := queryPaths(t, prQuery)
	want := []string{
		"repository.nameWithOwner",
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
		"isCrossRepository", "author.__typename", "author.login", "author.avatarUrl"} {
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

// Each argument changes what GitHub sends: without states: OPEN, closed PRs
// come back; with first: instead of last:, old reviews come back instead of
// the newest; without author: $me, mine is a copy of the last 100 reviews.
// reviewRequests reads 100, the API maximum: CODEOWNERS can request many.
func TestQueryArguments(t *testing.T) {
	_, got := queryPaths(t, prQuery)
	want := map[string]string{
		"repository":              "owner:$owner,name:$name",
		"repository.pullRequests": "states:OPEN,first:100,after:$cursor",
		"repository.pullRequests.nodes.reviewRequests": "first:100",
		"repository.pullRequests.nodes.reviews":        "last:100",
		"repository.pullRequests.nodes.mine":           "author:$me,last:100",
	}
	if !maps.Equal(got, want) {
		t.Errorf("arguments = %v\nwant %v", got, want)
	}
}

func TestQueryPaths(t *testing.T) {
	paths, args := queryPaths(t, `query($a: String) { r(x: $a, y: [1, 2]) { b c: d(n: 1) { e } g ... on T { f } } }`)
	for _, w := range []string{"r.b", "r.c.e", "r.g", "r.T.f"} {
		if !paths[w] {
			t.Errorf("no path %s in %v", w, paths)
		}
	}
	if len(paths) != 4 {
		t.Errorf("paths = %v, want 4", paths)
	}
	if want := map[string]string{"r": "x:$a,y:[1,2]", "r.c": "n:1"}; !maps.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

// queryPaths gives the dotted path of each leaf field of a GraphQL query,
// and the arguments of each field with arguments, without spaces. An alias
// names its field, and an inline fragment adds its type name. The variables
// of the operation are not recorded. It knows only the syntax that
// query.graphql uses: one unnamed operation, no comments.
func queryPaths(t *testing.T, q string) (paths map[string]bool, args map[string]string) {
	t.Helper()
	tokens := regexp.MustCompile(`\.\.\.|\$?[A-Za-z_][A-Za-z0-9_]*|"[^"]*"|\S`).FindAllString(q, -1)
	paths, args = map[string]bool{}, map[string]string{}
	var stack []string
	last := ""
	path := func() string { return strings.Join(append(stack[1:len(stack):len(stack)], last), ".") }
	leaf := func() {
		if last != "" {
			paths[path()] = true
		}
	}
	for i := 0; i < len(tokens); i++ {
		switch tok := tokens[i]; tok {
		case "(":
			var text []string
			for depth := 1; ; {
				i++
				if tokens[i] == "(" {
					depth++
				} else if tokens[i] == ")" {
					if depth--; depth == 0 {
						break
					}
				}
				text = append(text, tokens[i])
			}
			if len(stack) > 0 {
				args[path()] = strings.Join(text, "")
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
	return paths, args
}
