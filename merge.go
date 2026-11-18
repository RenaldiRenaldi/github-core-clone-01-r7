package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func (f *forge) git(repoPath string, args ...string) ([]byte, error) {
	c := exec.Command("git", append([]string{"--git-dir=" + repoPath}, args...)...)
	return c.Output()
}

func (f *forge) specialRepoAPI(w http.ResponseWriter, r *http.Request, q *repo, p []string) bool {
	if f.reviews == nil {
		f.reviews = map[string][]review{}
	}
	if f.protections == nil {
		f.protections = map[string]protection{}
	}
	if len(p) == 9 && p[5] == "git" && p[6] == "refs" && p[7] == "heads" {
		if r.Method != "GET" {
			writeJSON(w, 405, map[string]string{"message": "Method not allowed"})
			return true
		}
		sha, err := f.git(filepath.Join(f.gitRoot, q.Owner, q.Name+".git"), "rev-parse", "refs/heads/"+p[8])
		if err != nil {
			writeJSON(w, 404, map[string]string{"message": "branch not found"})
			return true
		}
		writeJSON(w, 200, map[string]string{"ref": "refs/heads/" + p[8], "sha": strings.TrimSpace(string(sha))})
		return true
	}
	if len(p) == 8 && p[5] == "branches" && p[7] == "protection" {
		key := q.Owner + "/" + q.Name + ":" + p[6]
		if r.Method == "PUT" {
			in := decode(r)
			f.protections[key] = protection{RequiredApprovingReviews: int(numberVal(in["required_approving_reviews"])), RequirePullRequest: boolVal(in["require_pull_request"])}
			writeJSON(w, 200, f.protections[key])
			return true
		}
		if r.Method == "GET" {
			writeJSON(w, 200, f.protections[key])
			return true
		}
	}
	if len(p) >= 7 && p[5] == "pulls" {
		var n int
		fmt.Sscan(p[6], &n)
		var pr *item
		for _, x := range q.PRs {
			if x.Number == n {
				pr = x
				break
			}
		}
		if pr == nil {
			writeJSON(w, 404, map[string]string{"message": "pull request not found"})
			return true
		}
		key := q.Owner + "/" + q.Name + ":" + fmt.Sprint(n)
		if len(p) == 8 && p[7] == "reviews" {
			if r.Method == "GET" {
				writeJSON(w, 200, f.reviews[key])
				return true
			}
			if r.Method == "POST" {
				in := decode(r)
				v := review{Reviewer: stringVal(in["reviewer"]), State: strings.ToLower(stringVal(in["event"])), Body: stringVal(in["body"])}
				if v.State == "" {
					v.State = strings.ToLower(stringVal(in["state"]))
				}
				if v.State != "approve" && v.State != "approved" && v.State != "request_changes" && v.State != "comment" {
					writeJSON(w, 422, map[string]string{"message": "event must be approve, request_changes, or comment"})
					return true
				}
				if v.State == "approve" {
					v.State = "approved"
				}
				f.reviews[key] = append(f.reviews[key], v)
				writeJSON(w, 201, v)
				return true
			}
		}
		if len(p) == 8 && p[7] == "merge" && r.Method == "POST" {
			base := pr.Base
			if base == "" {
				base = "main"
			}
			prot := f.protections[q.Owner+"/"+q.Name+":"+base]
			approvals := 0
			for _, v := range f.reviews[key] {
				if v.State == "approved" {
					approvals++
				}
			}
			if approvals < prot.RequiredApprovingReviews {
				writeJSON(w, 403, map[string]string{"message": "branch protection requires an approving review"})
				return true
			}
			sha, conflicts, err := f.mergeBranches(q, pr.Head, base)
			if len(conflicts) > 0 {
				writeJSON(w, 409, map[string]any{"message": "merge conflict", "conflicting_paths": conflicts})
				return true
			}
			if err != nil {
				writeJSON(w, 422, map[string]string{"message": err.Error()})
				return true
			}
			pr.State = "closed"
			writeJSON(w, 200, map[string]any{"sha": sha, "merged": true})
			return true
		}
	}
	return false
}

func numberVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
func boolVal(v any) bool { b, _ := v.(bool); return b }

type gitEntry struct{ mode, typ, oid string }

func (f *forge) tree(repoPath, rev string) (map[string]gitEntry, error) {
	b, err := f.git(repoPath, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return nil, err
	}
	out := map[string]gitEntry{}
	for _, rec := range bytes.Split(b, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		a := bytes.SplitN(rec, []byte("\t"), 2)
		meta := strings.Fields(string(a[0]))
		if len(meta) == 3 {
			out[string(a[1])] = gitEntry{meta[0], meta[1], meta[2]}
		}
	}
	return out, nil
}
func (f *forge) mergeBranches(q *repo, head, base string) (string, []string, error) {
	path := filepath.Join(f.gitRoot, q.Owner, q.Name+".git")
	baseRev := "refs/heads/" + base
	headRev := "refs/heads/" + head
	mb, err := f.git(path, "merge-base", baseRev, headRev)
	if err != nil {
		return "", nil, fmt.Errorf("branches do not share a merge base")
	}
	ancestor := strings.TrimSpace(string(mb))
	b, err := f.tree(path, ancestor)
	if err != nil {
		return "", nil, err
	}
	h, err := f.tree(path, headRev)
	if err != nil {
		return "", nil, fmt.Errorf("head branch not found")
	}
	d, err := f.tree(path, baseRev)
	if err != nil {
		return "", nil, fmt.Errorf("base branch not found")
	}
	merged := map[string]gitEntry{}
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range h {
		keys[k] = true
	}
	for k := range d {
		keys[k] = true
	}
	conflicts := []string{}
	for k := range keys {
		bv, bok := b[k]
		hv, hok := h[k]
		dv, dok := d[k]
		hc := hok != bok || hv != bv
		dc := dok != bok || dv != bv
		switch {
		case hc && dc && (hok != dok || hv != dv):
			conflicts = append(conflicts, k)
		case hc:
			if hok {
				merged[k] = hv
			}
		case dc:
			if dok {
				merged[k] = dv
			}
		default:
			if bok {
				merged[k] = bv
			}
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return "", conflicts, nil
	}
	paths := make([]string, 0, len(merged))
	for k := range merged {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	args := []string{"mktree", "-z"}
	var input bytes.Buffer
	for _, name := range paths {
		e := merged[name]
		fmt.Fprintf(&input, "%s %s %s\t%s\x00", e.mode, e.typ, e.oid, name)
	}
	c := exec.Command("git", append([]string{"--git-dir=" + path}, args...)...)
	c.Stdin = &input
	tree, err := c.Output()
	if err != nil {
		return "", nil, err
	}
	commitCmd := exec.Command("git", "--git-dir="+path, "commit-tree", strings.TrimSpace(string(tree)), "-p", strings.TrimSpace(string(dRev(path, baseRev))), "-p", strings.TrimSpace(string(dRev(path, headRev))), "-m", "Merge pull request")
	commitCmd.Env = append([]string{}, "GIT_AUTHOR_NAME=Go Forge", "GIT_AUTHOR_EMAIL=forge@example.test", "GIT_COMMITTER_NAME=Go Forge", "GIT_COMMITTER_EMAIL=forge@example.test")
	commit, err := commitCmd.Output()
	if err != nil {
		return "", nil, err
	}
	sha := strings.TrimSpace(string(commit))
	old := strings.TrimSpace(string(dRev(path, baseRev)))
	if _, err = f.git(path, "update-ref", "refs/heads/"+base, sha, old); err != nil {
		return "", nil, fmt.Errorf("base branch changed during merge")
	}
	return sha, nil, nil
}
func dRev(path, rev string) []byte {
	b, _ := exec.Command("git", "--git-dir="+path, "rev-parse", rev).Output()
	return b
}
