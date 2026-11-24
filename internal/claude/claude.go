// Package claude finds the Claude Code sessions on this machine (Linux only),
// and the GitHub repo and branch of each one.
package claude

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// Find gives one session per distinct directory of the "claude" processes in
// the proc root, sorted by directory. It skips each process that it cannot
// read or whose directory has no GitHub branch. The error tells only that the
// proc root cannot be listed.
func Find(ctx context.Context, proc string) ([]Session, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if strings.Trim(e.Name(), "0123456789") != "" {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(proc, e.Name(), "comm"))
		if err != nil || strings.TrimSuffix(string(comm), "\n") != "claude" {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(proc, e.Name(), "cwd"))
		if err != nil {
			continue
		}
		dirs = append(dirs, cwd)
	}
	slices.Sort(dirs)
	var out []Session
	for _, dir := range slices.Compact(dirs) {
		if repo, branches, ok := lookup(ctx, dir); ok {
			out = append(out, Session{Dir: dir, Repo: repo, Branches: branches})
		}
	}
	return out, nil
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
	return "", false
}

// gitOut runs git in dir and gives its output without the final newline. It
// keeps the other bytes: a branch name can end with a non-ASCII space.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}
