package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// exportState emits a canonical snapshot: slices are copied in stable order,
// map keys are sorted by encoding/json, and no clock or random values appear.
func (f *forge) exportState(w http.ResponseWriter) {
	type exportedRepo struct {
		Owner string `json:"owner"`
		Name string `json:"name"`
		Description string `json:"description"`
		ID int `json:"id"`
		Refs map[string]string `json:"refs"`
		Issues []*item `json:"issues"`
		PRs []*item `json:"pull_requests"`
	}
	type snapshot struct {
		Repositories []exportedRepo `json:"repositories"`
		Reviews map[string][]review `json:"reviews"`
		Protections map[string]protection `json:"protections"`
	}
	f.mu.RLock()
	keys := make([]string, 0, len(f.repos))
	for k := range f.repos { keys = append(keys, k) }
	sort.Strings(keys)
	out := snapshot{Repositories: make([]exportedRepo, 0, len(keys)), Reviews: map[string][]review{}, Protections: map[string]protection{}}
	for _, k := range keys {
		q := f.repos[k]
		e := exportedRepo{Owner:q.Owner, Name:q.Name, Description:q.Description, ID:q.ID, Refs:map[string]string{}, Issues:append([]*item(nil), q.Issues...), PRs:append([]*item(nil), q.PRs...)}
		root := filepath.Join(f.gitRoot, q.Owner, q.Name+".git")
		_ = filepath.WalkDir(filepath.Join(root, "refs"), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() { return nil }
			b, e2 := os.ReadFile(path)
			if e2 == nil { rel, _ := filepath.Rel(root, path); e.Refs[filepath.ToSlash(rel)] = strings.TrimSpace(string(b)) }
			return nil
		})
		out.Repositories = append(out.Repositories, e)
	}
	for k, v := range f.reviews { out.Reviews[k] = append([]review(nil), v...) }
	for k, v := range f.protections { out.Protections[k] = v }
	f.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
