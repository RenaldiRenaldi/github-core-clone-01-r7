package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git's bundled CGI backend implements the smart HTTP wire protocol, including
// pkt-lines, side-band pack streams, and receive-pack ref transactions.
func (f *forge) serveGit(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.Contains(path, ".git/") {
		return false
	}
	if !strings.HasSuffix(path, "/info/refs") && !strings.HasSuffix(path, "/git-upload-pack") && !strings.HasSuffix(path, "/git-receive-pack") {
		return false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return true
	}
	project := filepath.Join(f.gitRoot, parts[0], strings.TrimSuffix(parts[1], "/info/refs"))
	if strings.HasSuffix(parts[1], "/git-upload-pack") {
		project = filepath.Join(f.gitRoot, strings.TrimSuffix(path, "/git-upload-pack"))
	}
	if strings.HasSuffix(parts[1], "/git-receive-pack") {
		project = filepath.Join(f.gitRoot, strings.TrimSuffix(path, "/git-receive-pack"))
	}
	if _, err := os.Stat(project); err != nil {
		http.NotFound(w, r)
		return true
	}
	env := append(os.Environ(), "GIT_PROJECT_ROOT="+f.gitRoot, "GIT_HTTP_EXPORT_ALL=1", "PATH_INFO=/"+path, "REMOTE_USER=", "REQUEST_METHOD="+r.Method, "QUERY_STRING="+r.URL.RawQuery, "CONTENT_TYPE="+r.Header.Get("Content-Type"), "CONTENT_LENGTH="+r.Header.Get("Content-Length"))
	if proto := r.Header.Get("Git-Protocol"); proto != "" {
		env = append(env, "HTTP_GIT_PROTOCOL="+proto)
	}
	cmd := exec.Command("git", "http-backend")
	cmd.Env = env
	cmd.Stdin = r.Body
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "git backend failed", http.StatusBadGateway)
		return true
	}
	i := bytes.Index(out, []byte("\r\n\r\n"))
	sep := 4
	if i < 0 {
		i = bytes.Index(out, []byte("\n\n"))
		sep = 2
	}
	if i < 0 {
		http.Error(w, "invalid git backend response", 502)
		return true
	}
	for _, line := range strings.Split(string(out[:i]), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			var code int
			fmt.Sscanf(strings.TrimPrefix(line, "Status:"), "%d", &code)
			if code != 0 {
				w.WriteHeader(code)
			}
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			w.Header().Add(k, strings.TrimSpace(v))
		}
	}
	w.Write(out[i+sep:])
	return true
}

// seedDemo creates a deterministic bare repository with a short history.
func (f *forge) seedDemo() {
	// Keep the in-memory API fixture and Git fixture aligned on the demo repo.
	work := filepath.Join(f.gitRoot, "octocat", "demo-work")
	bare := filepath.Join(f.gitRoot, "octocat", "hello-world.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0755); err != nil {
		panic(err)
	}
	run := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Demo", "GIT_AUTHOR_EMAIL=demo@example.test", "GIT_COMMITTER_NAME=Demo", "GIT_COMMITTER_EMAIL=demo@example.test", "GIT_AUTHOR_DATE=2025-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2025-01-01T00:00:00Z")
		if b, e := c.CombinedOutput(); e != nil {
			panic(fmt.Sprintf("git %v: %s (%v)", args, b, e))
		}
	}
	if err := os.MkdirAll(work, 0755); err != nil {
		panic(err)
	}
	run(work, "init", "-b", "main")
	run(work, "config", "user.name", "Demo")
	run(work, "config", "user.email", "demo@example.test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Hello World\n\nA tiny demo repository.\n"), 0644); err != nil {
		panic(err)
	}
	run(work, "add", "README.md")
	run(work, "commit", "-m", "Initial demo")
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("Hello from the Go forge.\n"), 0644); err != nil {
		panic(err)
	}
	run(work, "add", "hello.txt")
	run(work, "commit", "-m", "Add greeting")
	run(work, "clone", "--bare", ".", bare)
	run(bare, "config", "http.receivepack", "true")
	_ = os.RemoveAll(work)
}
