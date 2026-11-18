package main

import (
	"bytes"
	"testing"
)

func TestObjectStoreWriteReadKinds(t *testing.T) {
	s := newObjectStore()
	for _, tc := range []struct {
		kind string
		data []byte
	}{
		{"blob", []byte("file contents\n")},
		{"tree", []byte("100644 file\x00" + string(bytes.Repeat([]byte{1}, 20)))},
		{"commit", []byte("tree 0123456789012345678901234567890123456789\n\nsubject\n")},
	} {
		id, err := s.WriteObject(tc.kind, tc.data)
		if err != nil {
			t.Fatal(err)
		}
		kind, data, err := s.ReadObject(id)
		if err != nil || kind != tc.kind || !bytes.Equal(data, tc.data) {
			t.Fatalf("round trip %s: kind=%s data=%q err=%v", tc.kind, kind, data, err)
		}
	}
}

func TestObjectStoreRefCAS(t *testing.T) {
	s := newObjectStore()
	a, _ := s.WriteObject("blob", []byte("a"))
	b, _ := s.WriteObject("blob", []byte("b"))
	if err := s.UpdateRef("refs/heads/main", a, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef("refs/heads/main", b, a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRef("refs/heads/main", a, a); err == nil {
		t.Fatal("stale CAS unexpectedly accepted")
	}
	if got, ok := s.ReadRef("refs/heads/main"); !ok || got != b {
		t.Fatalf("ref = %q, %v; want %q", got, ok, b)
	}
}

func TestSeededDemoObjectSHAsAreStable(t *testing.T) {
	a, b := testForge(t), testForge(t)
	x, _ := a.objects.ReadRef("refs/heads/main")
	y, _ := b.objects.ReadRef("refs/heads/main")
	if x != y {
		t.Fatalf("seed changed across runs: %s != %s", x, y)
	}
	kind, data, err := a.objects.ReadObject(x)
	if err != nil || kind != "commit" || !bytes.Contains(data, []byte("Add greeting")) {
		t.Fatalf("seed tip = %s %q %v", kind, data, err)
	}
}
