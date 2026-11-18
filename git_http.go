package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
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
	if strings.HasSuffix(parts[1], ".git/info/refs") {
		service := r.URL.Query().Get("service")
		if service == "git-upload-pack" || service == "git-receive-pack" {
			f.advertiseRefs(w, service)
			return true
		}
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
	var receiveBody []byte
	if strings.HasSuffix(parts[1], "/git-receive-pack") {
		var err error
		receiveBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unable to read receive-pack request", 400)
			return true
		}
		if stale, ref := f.staleReceivePack(project, receiveBody); stale {
			writeJSON(w, 409, map[string]string{"message": "stale ref update rejected", "ref": ref})
			return true
		}
		r.Body = io.NopCloser(bytes.NewReader(receiveBody))
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

// Git's receive-pack command list starts with old/new/ref pkt-lines. Check all
// advertised old values before invoking the backend, so a stale multi-ref push
// cannot partially update any refs.
func (f *forge) staleReceivePack(repoPath string, body []byte) (bool, string) {
	r := bytes.NewReader(body)
	for {
		line, err := DecodePktLine(r)
		if err != nil || line == nil {
			return false, ""
		}
		if i := bytes.IndexByte(line, 0); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(string(line))
		if len(fields) < 3 {
			return false, ""
		}
		old, ref := fields[0], fields[2]
		current, err := exec.Command("git", "--git-dir="+repoPath, "rev-parse", "--verify", ref).Output()
		if err != nil {
			current = []byte("0000000000000000000000000000000000000000")
		}
		if strings.TrimSpace(string(current)) != old {
			return true, ref
		}
	}
}

// seedDemo creates a deterministic bare repository with a short history.
func (f *forge) seedDemo() {
	store := newObjectStore()
	f.objects = store
	blobReadme, _ := store.WriteObject("blob", []byte("# Hello World\n\nA tiny demo repository.\n"))
	blobHello, _ := store.WriteObject("blob", []byte("Hello from the Go forge.\n"))
	readmeTree, _ := store.WriteObject("tree", []byte(treeEntry("100644", "README.md", blobReadme)))
	first, _ := store.WriteObject("commit", []byte(demoCommit(readmeTree, "", "Initial demo")))
	secondTree, _ := store.WriteObject("tree", []byte(treeEntry("100644", "README.md", blobReadme)+treeEntry("100644", "hello.txt", blobHello)))
	second, _ := store.WriteObject("commit", []byte(demoCommit(secondTree, first, "Add greeting")))
	if err := store.UpdateRef("refs/heads/main", second, ""); err != nil {
		panic(err)
	}
	// Keep the existing CGI-backed transfer and merge paths usable without
	// invoking Git during startup: publish canonical loose files directly.
	bare := filepath.Join(f.gitRoot, "octocat", "hello-world.git")
	if err := os.MkdirAll(filepath.Join(bare, "objects"), 0755); err != nil {
		panic(err)
	}
	for id, encoded := range store.objects {
		dir := filepath.Join(bare, "objects", id[:2])
		if err := os.MkdirAll(dir, 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id[2:]), encoded, 0444); err != nil {
			panic(err)
		}
	}
	refPath := filepath.Join(bare, "refs", "heads", "main")
	if err := os.MkdirAll(filepath.Dir(refPath), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(refPath, []byte(second+"\n"), 0644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		panic(err)
	}
	config := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = true\n[http]\n\treceivepack = true\n"
	if err := os.WriteFile(filepath.Join(bare, "config"), []byte(config), 0644); err != nil {
		panic(err)
	}
}

func treeEntry(mode, name, id string) string { return mode + " " + name + "\x00" + string(mustHex(id)) }
func mustHex(id string) []byte {
	b, err := hex.DecodeString(id)
	if err != nil {
		panic(err)
	}
	return b
}
func demoCommit(tree, parent, message string) string {
	body := "tree " + tree + "\n"
	if parent != "" {
		body += "parent " + parent + "\n"
	}
	return body + "author Demo <demo@example.test> 1735689600 +0000\ncommitter Demo <demo@example.test> 1735689600 +0000\n\n" + message + "\n"
}

func (f *forge) advertiseRefs(w http.ResponseWriter, service string) {
	if f.objects == nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(EncodePktLine([]byte("# service=" + service + "\n")))
	_, _ = w.Write(EncodeControlPacket(PacketFlush))
	refs := f.objects.HeadRefs()
	capabilities := "multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta agent=git-core-clone/1"
	if len(refs) > 0 {
		_, _ = w.Write(EncodePktLine([]byte(refs[0].id + " " + refs[0].name + "\x00" + capabilities + "\n")))
		for _, ref := range refs[1:] {
			_, _ = w.Write(EncodePktLine([]byte(ref.id + " " + ref.name + "\n")))
		}
	}
	_, _ = w.Write(EncodeControlPacket(PacketFlush))
}
