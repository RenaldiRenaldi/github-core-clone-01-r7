package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
	if _, err := os.Stat(filepath.Join(f.gitRoot, gitNamespace(parts)+".git")); err != nil {
		http.NotFound(w, r)
		return true
	}
	if strings.HasSuffix(parts[1], ".git/info/refs") {
		service := r.URL.Query().Get("service")
		if service == "git-upload-pack" || service == "git-receive-pack" {
			repoName := strings.TrimSuffix(strings.SplitN(parts[1], "/", 2)[0], ".git")
			f.advertiseRefsFor(w, service, parts[0]+"/"+repoName)
			return true
		}
	}
	if f.objects == nil {
		http.NotFound(w, r)
		return true
	}
	if strings.HasSuffix(parts[1], "/git-upload-pack") {
		f.handleUploadPackFor(w, r, gitNamespace(parts))
	} else if strings.HasSuffix(parts[1], "/git-receive-pack") {
		f.handleReceivePackFor(w, r, gitNamespace(parts))
	} else {
		http.NotFound(w, r)
	}
	return true
}
func gitNamespace(parts []string) string {
	repoName := strings.TrimSuffix(strings.SplitN(parts[1], "/", 2)[0], ".git")
	return parts[0] + "/" + repoName
}

func (f *forge) handleUploadPack(w http.ResponseWriter, r *http.Request) {
	f.handleUploadPackFor(w, r, "")
}
func (f *forge) handleUploadPackFor(w http.ResponseWriter, r *http.Request, namespace string) {
	body, e := io.ReadAll(r.Body)
	if e != nil {
		http.Error(w, "bad request", 400)
		return
	}
	rd := bytes.NewReader(body)
	wants, haves := []string{}, []string{}
	sideband := false
	for {
		line, e := DecodePktLine(rd)
		if e != nil || line == nil {
			break
		}
		s := strings.TrimSpace(string(line))
		if bytes.Contains(line, []byte("side-band")) {
			sideband = true
		}
		if z := strings.IndexByte(s, 0); z >= 0 {
			s = s[:z]
		}
		fields := strings.Fields(s)
		if len(fields) >= 2 && fields[0] == "want" {
			wants = append(wants, fields[1])
		}
		if len(fields) >= 2 && fields[0] == "have" {
			haves = append(haves, fields[1])
		}
	}
	ids := f.objects.reachable(wants)
	excluded := map[string]bool{}
	for _, id := range f.objects.reachable(haves) {
		excluded[id] = true
	}
	filtered := ids[:0]
	for _, id := range ids {
		if !excluded[id] {
			filtered = append(filtered, id)
		}
	}
	pack, e := f.objects.makePack(filtered)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	if len(haves) > 0 {
		_, _ = w.Write(EncodePktLine([]byte("ACK " + haves[len(haves)-1] + "\n")))
	} else {
		_, _ = w.Write(EncodePktLine([]byte("NAK\n")))
	}
	if sideband {
		for len(pack) > 0 {
			n := len(pack)
			if n > 65515 {
				n = 65515
			}
			payload := append([]byte{1}, pack[:n]...)
			_, _ = w.Write(EncodePktLine(payload))
			pack = pack[n:]
		}
		_, _ = w.Write(EncodeControlPacket(PacketFlush))
	} else {
		_, _ = w.Write(pack)
	}
}

func (f *forge) handleReceivePack(w http.ResponseWriter, r *http.Request) {
	f.handleReceivePackFor(w, r, "")
}
func (f *forge) handleReceivePackFor(w http.ResponseWriter, r *http.Request, namespace string) {
	body, e := io.ReadAll(r.Body)
	if e != nil {
		http.Error(w, "bad request", 400)
		return
	}
	rd := bytes.NewReader(body)
	type update struct{ old, new, ref string }
	updates := []update{}
	for {
		line, e := DecodePktLine(rd)
		if e != nil {
			http.Error(w, "malformed commands", 400)
			return
		}
		if line == nil {
			break
		}
		s := string(line)
		if i := strings.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		fields := strings.Fields(s)
		if len(fields) < 3 {
			http.Error(w, "malformed command", 400)
			return
		}
		updates = append(updates, update{fields[0], fields[1], fields[2]})
	}
	pack, e := io.ReadAll(rd)
	if e != nil {
		http.Error(w, "bad pack", 400)
		return
	}
	if len(pack) > 0 {
		if e = f.objects.parsePack(pack); e != nil {
			writeReceiveStatus(w, []byte("unpack "+e.Error()+"\n"), nil)
			return
		}
	}
	statuses := [][]byte{[]byte("unpack ok\n")}
	prepared := []refUpdate{}
	valid := true
	for _, u := range updates {
		current, ok := f.objects.ReadRefIn(namespace, u.ref)
		if !ok {
			current = "0000000000000000000000000000000000000000"
		}
		if current != u.old {
			statuses = append(statuses, []byte("ng "+u.ref+" stale info\n"))
			valid = false
			continue
		}
		if u.new == "0000000000000000000000000000000000000000" {
			statuses = append(statuses, []byte("ng "+u.ref+" ref deletion unsupported\n"))
			valid = false
			continue
		}
		if !f.objects.isAncestor(u.old, u.new) {
			statuses = append(statuses, []byte("ng "+u.ref+" non-fast-forward\n"))
			valid = false
			continue
		}
		expected := u.old
		if expected == "0000000000000000000000000000000000000000" {
			expected = ""
		}
		prepared = append(prepared, refUpdate{u.ref, u.new, expected})
	}
	if valid {
		if e := f.objects.UpdateRefsIn(namespace, prepared); e != nil {
			for _, u := range updates {
				statuses = append(statuses, []byte("ng "+u.ref+" "+e.Error()+"\n"))
			}
		} else {
			for _, u := range updates {
				statuses = append(statuses, []byte("ok "+u.ref+"\n"))
			}
		}
	} else {
		for _, u := range updates {
			if !bytes.Contains(bytes.Join(statuses, nil), []byte("ng "+u.ref+" ")) {
				statuses = append(statuses, []byte("ng "+u.ref+" atomic transaction aborted\n"))
			}
		}
	}
	writeReceiveStatus(w, nil, statuses)
}

func writeReceiveStatus(w http.ResponseWriter, first []byte, statuses [][]byte) {
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	if first != nil {
		statuses = append([][]byte{first}, statuses...)
	}
	var report bytes.Buffer
	for _, line := range statuses {
		report.Write(EncodePktLine(line))
	}
	report.Write(EncodeControlPacket(PacketFlush))
	_, _ = w.Write(EncodePktLine(append([]byte{1}, report.Bytes()...)))
	_, _ = w.Write(EncodeControlPacket(PacketFlush))
}

func (f *forge) staleReceivePack(_ string, body []byte) (bool, string) {
	r := bytes.NewReader(body)
	for {
		line, err := DecodePktLine(r)
		if err != nil || line == nil {
			return false, ""
		}
		text := string(line)
		if i := strings.IndexByte(text, 0); i >= 0 {
			text = text[:i]
		}
		fields := strings.Fields(text)
		if len(fields) < 3 {
			return false, ""
		}
		current, ok := f.objects.ReadRef(fields[2])
		if !ok {
			current = "0000000000000000000000000000000000000000"
		}
		if current != fields[0] {
			return true, fields[2]
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
	if err := store.UpdateRefIn("octocat/hello-world", "refs/heads/main", second, ""); err != nil {
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
	f.advertiseRefsFor(w, service, "")
}
func (f *forge) advertiseRefsFor(w http.ResponseWriter, service, namespace string) {
	if f.objects == nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(EncodePktLine([]byte("# service=" + service + "\n")))
	_, _ = w.Write(EncodeControlPacket(PacketFlush))
	refs := f.objects.HeadRefsIn(namespace)
	capabilities := "report-status delete-refs side-band-64k ofs-delta atomic agent=git-core-clone/1"
	if service == "git-upload-pack" {
		capabilities = "multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta symref=HEAD:refs/heads/main agent=git-core-clone/1"
	}
	if len(refs) > 0 {
		_, _ = w.Write(EncodePktLine([]byte(refs[0].id + " HEAD\x00" + capabilities + "\n")))
		for _, ref := range refs {
			_, _ = w.Write(EncodePktLine([]byte(ref.id + " " + ref.name + "\n")))
		}
	}
	_, _ = w.Write(EncodeControlPacket(PacketFlush))
}
