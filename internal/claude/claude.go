// Package claude finds the Claude Code sessions on this machine (Linux only),
// and the GitHub repo and branch of each one.
package claude

import (
	"context"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Session is a Claude Code process and the branch checked out in its directory.
type Session struct {
	Dir  string // the cwd of the process
	Repo string // "owner/name", as the remote URL gives it
	// Branches are the names the PR branch can have on the remote, without
	// "refs/heads/": the upstream's branch, then the local branch if its
	// name differs. A branch made from origin/main has the upstream main
	// until a push with -u, so the local name is needed too.
	Branches []string
}

// RepoOf gives the "owner/name" of a GitHub remote URL: scp-like
// ("[user@]github.com:owner/name") or a URL ("ssh://", "https://", with a
// user and a port or not), on github.com or ssh.github.com.
func RepoOf(remote string) (string, bool) {
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else {
		h, p, ok := strings.Cut(remote, ":")
		if !ok || strings.Contains(h, "/") {
			return "", false
		}
		host = h[strings.LastIndex(h, "@")+1:]
		path = p
	}
	if host = strings.ToLower(host); host != "github.com" && host != "ssh.github.com" {
		return "", false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return path, true
}

// waitDelay limits the wait for the output of a git that the end of the
// context killed, when a child of git keeps the output open.
const waitDelay = 100 * time.Millisecond

// Find gives one session per distinct directory of the "claude" processes in
// the proc root, sorted by directory. It skips each process that it cannot
// read, that ends during the git calls, or whose directory has no GitHub
// branch. The error tells only that the proc root cannot be listed.
func Find(ctx context.Context, proc string) ([]Session, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	pids := map[string][]string{} // directory → PIDs
	for _, e := range entries {
		if strings.Trim(e.Name(), "0123456789") != "" || !isClaude(proc, e.Name()) {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(proc, e.Name(), "cwd"))
		if err != nil {
			continue
		}
		pids[cwd] = append(pids[cwd], e.Name())
	}
	var out []Session
	for _, dir := range slices.Sorted(maps.Keys(pids)) {
		repo, branches, ok := lookup(ctx, dir)
		alive := slices.ContainsFunc(pids[dir], func(pid string) bool { return isClaude(proc, pid) })
		if ok && alive {
			out = append(out, Session{Dir: dir, Repo: repo, Branches: branches})
		}
	}
	return out, nil
}

// isClaude tells if the process pid runs Claude Code.
func isClaude(proc, pid string) bool {
	comm, err := os.ReadFile(filepath.Join(proc, pid, "comm"))
	return err == nil && strings.TrimSuffix(string(comm), "\n") == "claude"
}

// lookup gives the repo and the branch names of the checkout in dir: the
// upstream's remote and branch, then the local branch; with no upstream,
// origin and the local branch.
func lookup(ctx context.Context, dir string) (repo string, branches []string, ok bool) {
	ref, ok := headRef(ctx, dir)
	if !ok {
		return "", nil, false
	}
	up, err := gitOut(ctx, dir, "for-each-ref", "--format=%(upstream:remotename)%09%(upstream:remoteref)", ref)
	if err != nil {
		return "", nil, false
	}
	remote, remoteRef, _ := strings.Cut(up, "\t")
	if remote == "" {
		remote, remoteRef = "origin", ref
	}
	url, err := gitOut(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", nil, false
	}
	repo, ok = RepoOf(url)
	if !ok {
		return "", nil, false
	}
	branches = []string{strings.TrimPrefix(remoteRef, "refs/heads/")}
	if local := strings.TrimPrefix(ref, "refs/heads/"); local != branches[0] {
		branches = append(branches, local)
	}
	return repo, branches, true
}

// headRef gives the local branch of HEAD, such as "refs/heads/fix". A rebase
// detaches HEAD: it then gives the branch under rebase.
func headRef(ctx context.Context, dir string) (string, bool) {
	if ref, err := gitOut(ctx, dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		return ref, true
	}
	for _, name := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
		path, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", name)
		if err != nil {
			return "", false
		}
		if b, err := os.ReadFile(path); err == nil {
			if ref := strings.TrimSuffix(string(b), "\n"); strings.HasPrefix(ref, "refs/heads/") {
				return ref, true
			}
		}
	}
	// The rebase can end between the reads above: HEAD is then on the branch again.
	if ref, err := gitOut(ctx, dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		return ref, true
	}
	return "", false
}

// localEnv lists the variables that make git use another repo than the one
// of its directory: the output of "git rev-parse --local-env-vars" (git 2.53).
var localEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

// gitOut runs git in dir and gives its output without the final newline. It
// keeps the other bytes: a branch name can end with a non-ASCII space.
// It removes the localEnv variables from the environment of git.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(localEnv, name)
	})
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	return strings.TrimSuffix(string(out), "\n"), err
}
