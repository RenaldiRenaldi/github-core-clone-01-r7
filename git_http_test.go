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
	if !bytes.Contains(got, []byte("refs/heads/main")) || !strings.Contains(w.Header().Get("Content-Type"), "git-upload-pack-advertisement") {
		t.Fatalf("missing main ref or content type: %q / %q", got, w.Header().Get("Content-Type"))
	}
}

func TestSmartHTTPCloneRoundTrip(t *testing.T) {
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
