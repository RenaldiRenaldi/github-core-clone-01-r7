package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// objectStore keeps immutable Git objects and named refs in memory. Object IDs
// use Git's canonical header and SHA-1 format, while the compressed encoding
// matches loose objects should persistence be added later.
type objectStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
	refs    map[string]string
}

func newObjectStore() *objectStore {
	return &objectStore{objects: map[string][]byte{}, refs: map[string]string{}}
}

func (s *objectStore) WriteObject(kind string, content []byte) (string, error) {
	if kind != "blob" && kind != "tree" && kind != "commit" {
		return "", fmt.Errorf("unsupported object type %q", kind)
	}
	var raw bytes.Buffer
	fmt.Fprintf(&raw, "%s %d\x00", kind, len(content))
	raw.Write(content)
	hash := sha1.Sum(raw.Bytes())
	id := hex.EncodeToString(hash[:])
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	if _, err := z.Write(raw.Bytes()); err != nil {
		return "", err
	}
	if err := z.Close(); err != nil {
		return "", err
	}
	// Store the canonical compressed representation; readers validate it too.
	s.mu.Lock()
	s.objects[id] = append([]byte(nil), compressed.Bytes()...)
	s.mu.Unlock()
	return id, nil
}

func (s *objectStore) ReadObject(id string) (string, []byte, error) {
	s.mu.RLock()
	encoded, ok := s.objects[id]
	encoded = append([]byte(nil), encoded...)
	s.mu.RUnlock()
	if !ok {
		return "", nil, errors.New("object not found")
	}
	kind, content, err := DecodeLoose(encoded)
	if err != nil {
		return "", nil, err
	}
	return kind, content, nil
}

func (s *objectStore) UpdateRef(name, id, expected string) error {
	if len(id) != 40 {
		return errors.New("invalid object id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.refs[name]
	if current != expected {
		return fmt.Errorf("ref compare-and-swap failed: have %q, expected %q", current, expected)
	}
	if _, ok := s.objects[id]; !ok {
		return errors.New("ref target object does not exist")
	}
	s.refs[name] = id
	return nil
}

func (s *objectStore) ReadRef(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.refs[name]
	return id, ok
}
func (s *objectStore) HeadRefs() []struct{ name, id string } {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var refs []struct{ name, id string }
	for name, id := range s.refs {
		if len(name) >= 11 && name[:11] == "refs/heads/" {
			refs = append(refs, struct{ name, id string }{name, id})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].name < refs[j].name })
	return refs
}

// DecodeLoose is useful for validating the exact compressed loose-object form.
func DecodeLoose(data []byte) (string, []byte, error) {
	z, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", nil, err
	}
	defer z.Close()
	raw, err := io.ReadAll(z)
	if err != nil {
		return "", nil, err
	}
	i := bytes.IndexByte(raw, 0)
	if i < 0 {
		return "", nil, errors.New("bad loose object")
	}
	var kind string
	var size int
	if _, err := fmt.Sscanf(string(raw[:i]), "%s %d", &kind, &size); err != nil || size != len(raw)-i-1 {
		return "", nil, errors.New("bad loose object header")
	}
	return kind, raw[i+1:], nil
}
