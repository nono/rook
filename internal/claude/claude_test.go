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
	"time"
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
		{"https://user@github.com/o/r.git", "o/r", true},
		{"https://github.com/o/r/", "o/r", true},
		{"ssh://git@github.com:22/o/r.git", "o/r", true},
		{"ssh://git@ssh.github.com:443/o/r.git", "o/r", true},
		{"git@GitHub.com:o/r.git", "o/r", true},
		{"https://GITHUB.COM/o/r", "o/r", true},
		{"https://gitlab.com/o/r.git", "", false},
		{"https://github.com.example.com/o/r", "", false},
		{"git@github.com:o/r/x.git", "", false},
		{"git@github.com:o", "", false},
		{"https://github.com/o/.git", "", false},
		{"/srv/git/r.git", "", false},
	}
	for _, c := range cases {
		got, ok := repoOf(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("repoOf(%q) = %q, %v; want %q, %v", c.url, got, ok, c.want, c.ok)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// isolate keeps the git calls of the test, and those of Find, away from the
// git config of the user and from the GIT_DIR and the like of the environment.
func isolate(t *testing.T) {
	t.Helper()
	for _, v := range localEnv {
		t.Setenv(v, "")
		if err := os.Unsetenv(v); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
}

// fakeGit puts first in PATH a git that runs the sh script, then the real git.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\nexec " + real + ` "$@"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	isolate(t)
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
	e := repo(t, "git@github.com:o/r.git", "z")
	git(t, e, "remote", "add", "fork", "git@github.com:o/f.git")
	upstream(t, e, "z", "fork", "z")
	f := repo(t, "git@github.com:o/r.git", "w")
	upstream(t, f, "w", "origin", "feature\u00a0")
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
	process(t, proc, "20", "claude", e)
	process(t, proc, "21", "claude", f)
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
		{Dir: a, Repo: "o/r", Branches: []string{"sc-1234/fix", "fix"}},
		{Dir: sub, Repo: "o/r", Branches: []string{"sc-1234/fix", "fix"}},
		{Dir: b, Repo: "o/r", Branches: []string{"feat"}},
		{Dir: e, Repo: "o/f", Branches: []string{"z"}},
		{Dir: f, Repo: "o/r", Branches: []string{"feature\u00a0", "w"}},
	}
	slices.SortFunc(want, func(x, y Session) int { return strings.Compare(x.Dir, y.Dir) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find =\n%+v\nwant\n%+v", got, want)
	}
}

// A rebase that stops on a conflict detaches HEAD. The session stays on the
// branch under rebase, with each rebase backend.
func TestFindDuringRebase(t *testing.T) {
	isolate(t)
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
			if err := exec.Command("git", "-C", dir, "rebase", backend, "main").Run(); err == nil {
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
			want := []Session{{Dir: dir, Repo: "o/r", Branches: []string{"sc-9/fix", "fix"}}, {Dir: sub, Repo: "o/r", Branches: []string{"sc-9/fix", "fix"}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Find =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// one makes a fake /proc with one claude process in a repo on branch fix.
func one(t *testing.T) (proc, dir string) {
	t.Helper()
	dir = repo(t, "git@github.com:o/r.git", "fix")
	proc = t.TempDir()
	process(t, proc, "10", "claude", dir)
	return proc, dir
}

// A rebase can end between the read of HEAD and the read of the rebase
// files. HEAD is then on the branch again.
func TestFindRebaseEndsBetweenReads(t *testing.T) {
	isolate(t)
	proc, dir := one(t)
	mark := filepath.Join(t.TempDir(), "mark")
	fakeGit(t, `if [ "$3" = symbolic-ref ] && [ ! -e `+mark+` ]; then touch `+mark+`; exit 1; fi`)
	got, err := Find(context.Background(), proc)
	if want := []Session{{Dir: dir, Repo: "o/r", Branches: []string{"fix"}}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Find = %+v, %v; want %+v", got, err, want)
	}
}

// A process that ends during the git calls is not a session.
func TestFindProcessEndsDuringLookup(t *testing.T) {
	isolate(t)
	proc, _ := one(t)
	fakeGit(t, "rm -rf "+filepath.Join(proc, "10"))
	if got, err := Find(context.Background(), proc); err != nil || len(got) != 0 {
		t.Fatalf("Find = %+v, %v; want none", got, err)
	}
}

// GIT_DIR in the environment of rook must not replace the repo of the session.
func TestFindIgnoresGitDir(t *testing.T) {
	isolate(t)
	proc, dir := one(t)
	other := repo(t, "git@github.com:o/wrong.git", "unrelated")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	got, err := Find(context.Background(), proc)
	if want := []Session{{Dir: dir, Repo: "o/r", Branches: []string{"fix"}}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Find = %+v, %v; want %+v", got, err, want)
	}
}

// A git that hangs, with a child that keeps its output open, must not hold
// Find past the end of the context.
func TestFindStopsSlowGit(t *testing.T) {
	isolate(t)
	proc, _ := one(t)
	fakeGit(t, "sleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got, err := Find(ctx, proc); err != nil || len(got) != 0 {
		t.Fatalf("Find = %+v, %v; want none", got, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Find took %s", d)
	}
}

func TestFindNoProc(t *testing.T) {
	if _, err := Find(context.Background(), "/does/not/exist"); err == nil {
		t.Fatal("Find on a missing /proc: want an error")
	}
}
