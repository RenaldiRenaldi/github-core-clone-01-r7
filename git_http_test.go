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
	got, ok := f.objects.ReadRefIn("octocat/hello-world", "refs/heads/main")
	_, commitData, err := f.objects.ReadObject(got)
	if !ok || err != nil || !bytes.Contains(commitData, []byte("accepted CAS push")) {
		t.Fatalf("stale push changed ref: %q %s %v", got, commitData, err)
	}
	remote, err := exec.Command("git", "ls-remote", s.URL+"/octocat/hello-world.git", "refs/heads/main").CombinedOutput()
	if err != nil || !strings.Contains(string(remote), got) {
		t.Fatalf("ls-remote: %v %s", err, remote)
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	cloneFresh := exec.Command("git", "clone", "-q", s.URL+"/octocat/hello-world.git", fresh)
	if out, e := cloneFresh.CombinedOutput(); e != nil {
		t.Fatalf("fresh clone: %v %s", e, out)
	}
	log, e := exec.Command("git", "-C", fresh, "log", "-1", "--format=%H").Output()
	if e != nil || strings.TrimSpace(string(log)) != got {
		t.Fatalf("fresh clone tip %q want %s: %v", log, got, e)
	}
}

func TestPackRoundTripAndUploadHandler(t *testing.T) {
	f := testForge(t)
	tip, _ := f.objects.ReadRef("refs/heads/main")
	ids := f.objects.reachable([]string{tip})
	pack, err := f.objects.makePack(ids)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		idx := exec.Command("git", "index-pack", "--stdin")
		idx.Stdin = bytes.NewReader(pack)
		if out, err := idx.CombinedOutput(); err != nil {
			t.Fatalf("git index-pack: %v %s", err, out)
		}
	}
	copyStore := newObjectStore()
	if err = copyStore.parsePack(pack); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		k, a, e := f.objects.ReadObject(id)
		k2, b, e2 := copyStore.ReadObject(id)
		if e != nil || e2 != nil || k != k2 || !bytes.Equal(a, b) {
			t.Fatalf("pack roundtrip %s: %v %v", id, e, e2)
		}
	}
	var req bytes.Buffer
	req.Write(EncodePktLine([]byte("want " + tip + " side-band-64k ofs-delta\n")))
	req.Write(EncodeControlPacket(PacketFlush))
	req.Write(EncodePktLine([]byte("done\n")))
	w := httptest.NewRecorder()
	f.handleUploadPack(w, httptest.NewRequest("POST", "/git-upload-pack", &req))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("PACK")) {
		t.Fatalf("upload response %d %q", w.Code, w.Body.Bytes())
	}
	resp := bytes.NewReader(w.Body.Bytes())
	_, _ = DecodePktLine(resp)
	var streamed bytes.Buffer
	for {
		p, e := DecodePktLine(resp)
		if e != nil || p == nil {
			break
		}
		if len(p) > 0 && p[0] == 1 {
			streamed.Write(p[1:])
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	verify := exec.Command("git", "index-pack", "--stdin")
	verify.Stdin = &streamed
	if out, e := verify.CombinedOutput(); e != nil {
		t.Fatalf("sideband pack invalid: %v %s", e, out)
	}
}

func TestDepthLimitedPackAndBoundary(t *testing.T) {
	f:=testForge(t); tip,_:=f.objects.ReadRef("refs/heads/main")
	ids,boundaries:=f.objects.reachableDepth([]string{tip},1)
	if len(boundaries)!=1 || boundaries[0]!=tip { t.Fatalf("depth-1 boundaries = %v",boundaries) }
	commits:=0; for _,id:=range ids { kind,_,err:=f.objects.ReadObject(id); if err==nil && kind=="commit" { commits++ } }
	if commits!=1 { t.Fatalf("depth-1 pack has %d commits, want 1",commits) }
	pack,err:=f.objects.makePack(ids); if err!=nil { t.Fatal(err) }; copy:=newObjectStore(); if err=copy.parsePack(pack); err!=nil { t.Fatal(err) }
	if _,_,err=copy.ReadObject(tip); err!=nil { t.Fatalf("tip absent from shallow pack: %v",err) }
	second,_:=f.objects.reachableDepth([]string{tip},2); commits=0; for _,id:=range second { kind,_,e:=f.objects.ReadObject(id); if e==nil && kind=="commit" { commits++ } }; if commits!=2 { t.Fatalf("depth-2 pack has %d commits, want 2",commits) }
}

func TestUploadPackCachesIdenticalPack(t *testing.T) {
	f:=testForge(t); tip,_:=f.objects.ReadRef("refs/heads/main")
	var request bytes.Buffer; request.Write(EncodePktLine([]byte("want "+tip+" side-band-64k\n"))); request.Write(EncodeControlPacket(PacketFlush)); request.Write(EncodePktLine([]byte("done\n")))
	run:=func() []byte { w:=httptest.NewRecorder(); f.handleUploadPack(w,httptest.NewRequest("POST","/git-upload-pack",bytes.NewReader(request.Bytes()))); if w.Code!=200 { t.Fatalf("upload status %d",w.Code) }; return append([]byte(nil),w.Body.Bytes()...) }
	a:=run(); b:=run(); if !bytes.Equal(a,b) { t.Fatal("identical upload-pack requests returned different bytes") }; if len(f.packCache)!=1 { t.Fatalf("cache entries = %d",len(f.packCache)) }
}

func TestReceivePackStoresObjectsAndRejectsStaleCAS(t *testing.T) {
	f := testForge(t)
	old, _ := f.objects.ReadRef("refs/heads/main")
	kind, oldCommit, e := f.objects.ReadObject(old)
	if e != nil || kind != "commit" {
		t.Fatal(e)
	}
	tree := strings.Fields(string(oldCommit))[1]
	client := newObjectStore()
	next, e := client.WriteObject("commit", []byte("tree "+tree+"\nparent "+old+"\nauthor T <t@x> 1735689601 +0000\ncommitter T <t@x> 1735689601 +0000\n\nreceive test\n"))
	if e != nil {
		t.Fatal(e)
	}
	pack, e := client.makePack([]string{next})
	if e != nil {
		t.Fatal(e)
	}
	command := func(from string) []byte {
		var b bytes.Buffer
		b.Write(EncodePktLine([]byte(from + " " + next + " refs/heads/main\x00report-status side-band-64k atomic\n")))
		b.Write(EncodeControlPacket(PacketFlush))
		b.Write(pack)
		return b.Bytes()
	}
	w := httptest.NewRecorder()
	f.handleReceivePack(w, httptest.NewRequest("POST", "/git-receive-pack", bytes.NewReader(command(old))))
	got, _ := f.objects.ReadRef("refs/heads/main")
	if got != next || !bytes.Contains(w.Body.Bytes(), []byte("ok refs/heads/main")) {
		t.Fatalf("receive result: ref=%s response=%q", got, w.Body.Bytes())
	}
	stale := "0000000000000000000000000000000000000000"
	w2 := httptest.NewRecorder()
	f.handleReceivePack(w2, httptest.NewRequest("POST", "/git-receive-pack", bytes.NewReader(command(stale))))
	after, _ := f.objects.ReadRef("refs/heads/main")
	if after != next || !bytes.Contains(w2.Body.Bytes(), []byte("ng refs/heads/main")) {
		t.Fatalf("stale update: ref=%s response=%q", after, w2.Body.Bytes())
	}
}
