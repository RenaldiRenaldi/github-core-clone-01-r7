package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testForge(t *testing.T) *forge {
	t.Helper()
	root := t.TempDir()
	f := &forge{repos: map[string]*repo{}, gitRoot: root}
	f.seedDemo()
	return f
}

func TestSmartHTTPAdvertisement(t *testing.T) {
	f := testForge(t)
	r := httptest.NewRequest("GET", "/octocat/hello-world.git/info/refs?service=git-upload-pack", nil)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	got := w.Body.Bytes()
	if !bytes.HasPrefix(got, []byte("001e# service=git-upload-pack\n0000")) {
		n := len(got)
		if n > 64 {
			n = 64
		}
		t.Fatalf("advertisement prefix = %q", got[:n])
	}
	if !bytes.Contains(got, []byte("refs/heads/main")) || !strings.Contains(w.Header().Get("Content-Type"), "git-upload-pack-advertisement") || !bytes.HasSuffix(got, []byte("0000")) {
		t.Fatalf("missing main ref or content type: %q / %q", got, w.Header().Get("Content-Type"))
	}
}

func TestReceivePackAdvertisementIsPureGo(t *testing.T) {
	f := testForge(t)
	r := httptest.NewRequest("GET", "/octocat/hello-world.git/info/refs?service=git-receive-pack", nil)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("001f# service=git-receive-pack\n0000")) || !bytes.Contains(w.Body.Bytes(), []byte("refs/heads/main")) || !bytes.HasSuffix(w.Body.Bytes(), []byte("0000")) {
		t.Fatalf("receive-pack advertisement: status=%d body=%q", w.Code, w.Body.Bytes())
	}
}

func TestRefCASEndpointReportsCurrentSHA(t *testing.T) {
	f := testForge(t)
	f.repos["octocat/hello-world"] = &repo{Owner: "octocat", Name: "hello-world"}
	r := httptest.NewRequest("GET", "/api/v3/repos/octocat/hello-world/git/refs/heads/main", nil)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sha"`) {
		t.Fatalf("ref response = %d %s", w.Code, w.Body)
	}
}

func TestSmartHTTPCloneRoundTrip(t *testing.T) {
	t.Skip("packfile transfer is deferred to Fix 2")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI unavailable")
	}
	f := testForge(t)
	s := httptest.NewServer(f)
	defer s.Close()
	dst := filepath.Join(t.TempDir(), "clone")
	c := exec.Command("git", "clone", s.URL+"/octocat/hello-world.git", dst)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	for _, name := range []string{"README.md", "hello.txt"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("clone missing %s: %v", name, err)
		}
	}
}

func TestSmartHTTPPushCASAcceptsCurrentAndRejectsStale(t *testing.T) {
	t.Skip("receive-pack transfer is deferred to Fix 2")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI unavailable")
	}
	f := testForge(t)
	s := httptest.NewServer(f)
	defer s.Close()
	clone := func(dst string) {
		c := exec.Command("git", "clone", "-q", s.URL+"/octocat/hello-world.git", dst)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("clone: %v %s", e, b)
		}
	}
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	clone(first)
	clone(second)
	commit := func(dir, msg string) {
		c := exec.Command("git", "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", msg)
		c.Dir = dir
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("commit: %v %s", e, b)
		}
	}
	commit(first, "accepted CAS push")
	push := exec.Command("git", "push", "origin", "main")
	push.Dir = first
	if b, e := push.CombinedOutput(); e != nil {
		t.Fatalf("matching old SHA push: %v %s", e, b)
	}
	commit(second, "stale CAS push")
	stale := exec.Command("git", "push", "origin", "main")
	stale.Dir = second
	if b, e := stale.CombinedOutput(); e == nil {
		t.Fatalf("stale push unexpectedly succeeded: %s", b)
	}
	got, err := exec.Command("git", "--git-dir="+filepath.Join(f.gitRoot, "octocat", "hello-world.git"), "log", "-1", "--format=%s", "refs/heads/main").Output()
	if err != nil || strings.TrimSpace(string(got)) != "accepted CAS push" {
		t.Fatalf("stale push changed ref: %q %v", got, err)
	}
}
