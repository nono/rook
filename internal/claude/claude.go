// Package claude finds the Claude Code sessions on this machine (Linux only),
// and the GitHub repo and branch of each one.
package claude

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Session is a Claude Code process and the branch checked out in its directory.
type Session struct {
	Dir    string // the cwd of the process
	Repo   string // "owner/name", as the remote URL gives it
	Branch string // the remote branch, without "refs/heads/"
}

var remoteURL = regexp.MustCompile(`^(?:git@github\.com:|ssh://git@github\.com/|https://github\.com/)([^/]+/[^/]+?)(?:\.git)?$`)

// RepoOf gives the "owner/name" of a GitHub remote URL.
func RepoOf(url string) (string, bool) {
	m := remoteURL.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return m[1], true
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
		if repo, branch, ok := lookup(ctx, dir); ok {
			out = append(out, Session{Dir: dir, Repo: repo, Branch: branch})
		}
	}
	return out, nil
}

// lookup gives the repo and the branch of the checkout in dir: the upstream if
// set, else the local branch on origin.
func lookup(ctx context.Context, dir string) (repo, branch string, ok bool) {
	ref, ok := headRef(ctx, dir)
	if !ok {
		return "", "", false
	}
	up, err := gitOut(ctx, dir, "for-each-ref", "--format=%(upstream:remotename)%09%(upstream:remoteref)", ref)
	if err != nil {
		return "", "", false
	}
	remote, remoteRef, _ := strings.Cut(up, "\t")
	if remote == "" {
		remote, remoteRef = "origin", ref
	}
	url, err := gitOut(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", "", false
	}
	repo, ok = RepoOf(url)
	if !ok {
		return "", "", false
	}
	return repo, strings.TrimPrefix(remoteRef, "refs/heads/"), true
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
			if ref := string(bytes.TrimSpace(b)); strings.HasPrefix(ref, "refs/heads/") {
				return ref, true
			}
		}
	}
	return "", false
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return string(bytes.TrimSpace(out)), err
}
