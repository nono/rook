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
	"sync"
	"time"
)

// Session is a Claude Code process and the branch checked out in its directory.
type Session struct {
	Dir    string // the cwd of the process
	Repo   string // "owner/name", as the remote URL gives it
	Branch string // the local branch, without "refs/heads/"
}

// repoOf gives the "owner/name" of a GitHub remote URL: scp-like
// ("[user@]github.com:owner/name") or a URL ("ssh://", "https://", with a
// user and a port or not), on github.com or ssh.github.com.
func repoOf(remote string) (string, bool) {
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
	// One lookup per directory, in parallel: a git that hangs in one
	// directory must not use the time of the others.
	dirs := slices.Sorted(maps.Keys(pids))
	found := make([]*Session, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Go(func() {
			repo, branch, ok := lookup(ctx, dir)
			alive := slices.ContainsFunc(pids[dir], func(pid string) bool { return isClaude(proc, pid) })
			if ok && alive {
				found[i] = &Session{Dir: dir, Repo: repo, Branch: branch}
			}
		})
	}
	wg.Wait()
	var out []Session
	for _, s := range found {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

// isClaude tells if the process pid runs Claude Code.
func isClaude(proc, pid string) bool {
	comm, err := os.ReadFile(filepath.Join(proc, pid, "comm"))
	return err == nil && strings.TrimSuffix(string(comm), "\n") == "claude"
}

// lookup gives the repo and the local branch of the checkout in dir. The PR
// head must have the name of the local branch: the upstream's branch name is
// not used, because a branch made from origin/main, or from the branch of
// another PR, keeps that upstream until a push with -u. The remote is the
// upstream's, else origin (also for an upstream on the local repo, ".").
func lookup(ctx context.Context, dir string) (repo, branch string, ok bool) {
	ref, ok := headRef(ctx, dir)
	if !ok {
		return "", "", false
	}
	remote, err := gitOut(ctx, dir, "for-each-ref", "--format=%(upstream:remotename)", ref)
	if err != nil {
		return "", "", false
	}
	if remote == "" || remote == "." {
		remote = "origin"
	}
	url, err := gitOut(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", "", false
	}
	if repo, ok = repoOf(url); !ok {
		return "", "", false
	}
	return repo, strings.TrimPrefix(ref, "refs/heads/"), true
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
