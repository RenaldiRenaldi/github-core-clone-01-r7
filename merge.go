package main

import (
	"fmt"
	"net/http"
	"strings"
)

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
		sha, ok := f.objects.ReadRef("refs/heads/" + p[8])
		if !ok {
			writeJSON(w, 404, map[string]string{"message": "branch not found"})
			return true
		}
		writeJSON(w, 200, map[string]string{"ref": "refs/heads/" + p[8], "sha": sha})
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
	return nil, fmt.Errorf("tree merge is not supported in this build")
}
func (f *forge) mergeBranches(q *repo, head, base string) (string, []string, error) {
	return "", nil, fmt.Errorf("pull request merges are not supported in this build")
}
