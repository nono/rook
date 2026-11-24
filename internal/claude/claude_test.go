package claude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRepoOf(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"git@github.com:o/r.git", "o/r", true},
		{"git@github.com:o/r", "o/r", true},
		{"ssh://git@github.com/o/r.git", "o/r", true},
		{"https://github.com/o/r.git", "o/r", true},
		{"https://github.com/o/r", "o/r", true},
		{"https://gitlab.com/o/r.git", "", false},
		{"git@github.com:o/r/x.git", "", false},
		{"/srv/git/r.git", "", false},
	}
	for _, c := range cases {
		got, ok := RepoOf(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("RepoOf(%q) = %q, %v; want %q, %v", c.url, got, ok, c.want, c.ok)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo makes a git repo with one commit, the remote origin at url, and the local branch checked out.
func repo(t *testing.T, url, local string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, dir, "remote", "add", "origin", url)
	git(t, dir, "checkout", "-q", "-b", local)
	return dir
}

func upstream(t *testing.T, dir, local, remote, branch string) {
	t.Helper()
	git(t, dir, "config", "branch."+local+".remote", remote)
	git(t, dir, "config", "branch."+local+".merge", "refs/heads/"+branch)
}

// process adds a process to the fake /proc. An empty comm writes no comm file.
func process(t *testing.T, proc, pid, comm, cwd string) {
	t.Helper()
	dir := filepath.Join(proc, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if comm != "" {
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	proc := t.TempDir()

	a := repo(t, "git@github.com:o/r.git", "fix")
	upstream(t, a, "fix", "origin", "sc-1234/fix")
	sub := filepath.Join(a, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := repo(t, "https://github.com/o/r", "feat")
	c := repo(t, "git@github.com:o/r.git", "x")
	git(t, c, "checkout", "-q", "--detach")
	d := repo(t, "git@github.com:o/r.git", "y")
	upstream(t, d, "y", ".", "main")
	plain := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}

	process(t, proc, "10", "claude", a)
	process(t, proc, "11", "claude", a)
	process(t, proc, "12", "claude", sub)
	process(t, proc, "13", "claude", b)
	process(t, proc, "14", "claude", c)
	process(t, proc, "15", "claude", d)
	process(t, proc, "16", "claude", plain)
	process(t, proc, "17", "claude", gone)
	process(t, proc, "18", "bash", a)
	process(t, proc, "self", "claude", a)
	process(t, proc, "19", "", a)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	got, err := Find(context.Background(), proc)
	if err != nil {
		t.Fatal(err)
	}
	want := []Session{
		{Dir: a, Repo: "o/r", Branch: "sc-1234/fix"},
		{Dir: sub, Repo: "o/r", Branch: "sc-1234/fix"},
		{Dir: b, Repo: "o/r", Branch: "feat"},
	}
	slices.SortFunc(want, func(x, y Session) int { return strings.Compare(x.Dir, y.Dir) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find =\n%+v\nwant\n%+v", got, want)
	}
}

// A rebase that stops on a conflict detaches HEAD. The session stays on the
// branch under rebase, with each rebase backend.
func TestFindDuringRebase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	for _, backend := range []string{"--merge", "--apply"} {
		t.Run(backend, func(t *testing.T) {
			dir := repo(t, "git@github.com:o/r.git", "fix")
			upstream(t, dir, "fix", "origin", "sc-9/fix")
			write := func(text string) {
				if err := os.WriteFile(filepath.Join(dir, "f"), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				git(t, dir, "add", "f")
				git(t, dir, "commit", "-q", "-m", text)
			}
			write("fix")
			git(t, dir, "checkout", "-q", "main")
			write("main")
			git(t, dir, "checkout", "-q", "fix")
			cmd := exec.Command("git", "-C", dir, "rebase", backend, "main")
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
			if err := cmd.Run(); err == nil {
				t.Fatal("rebase: want a conflict")
			}
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			proc := t.TempDir()
			process(t, proc, "10", "claude", dir)
			process(t, proc, "11", "claude", sub)

			got, err := Find(context.Background(), proc)
			if err != nil {
				t.Fatal(err)
			}
			want := []Session{{Dir: dir, Repo: "o/r", Branch: "sc-9/fix"}, {Dir: sub, Repo: "o/r", Branch: "sc-9/fix"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Find =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestFindNoProc(t *testing.T) {
	if _, err := Find(context.Background(), "/does/not/exist"); err == nil {
		t.Fatal("Find on a missing /proc: want an error")
	}
}
