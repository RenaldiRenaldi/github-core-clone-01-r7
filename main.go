package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type repo struct {
	Owner, Name, Description string
	ID                       int
	Issues                   []map[string]any
	Labels                   map[string]bool
}
type forge struct {
	repos       map[string]*repo
	next, issue int
	gitRoot     string
}

func (f *forge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.serveGit(w, r) {
		return
	}
	w.Header().Set("X-GitHub-Request-Id", "R7-FORGE")
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/healthz" {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
		tok := os.Getenv("MAKEFUN_SHOP_TOKEN")
		if tok != "" && r.Header.Get("Authorization") != "Bearer "+tok && r.Header.Get("Authorization") != "token "+tok {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
			return
		}
	}
	if r.URL.Path == "/api/v3/" {
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		json.NewEncoder(w).Encode(map[string]any{"current_user_url": "/api/v3/user"})
		return
	}
	if r.URL.Path == "/api/v3/meta" {
		json.NewEncoder(w).Encode(map[string]string{"installed_version": "r7-go"})
		return
	}
	if r.URL.Path == "/api/v3/user/repos" && r.Method == "POST" {
		var x map[string]any
		if json.NewDecoder(r.Body).Decode(&x) != nil {
			http.Error(w, "{\"message\":\"Malformed JSON\"}", 400)
			return
		}
		name, _ := x["name"].(string)
		owner := os.Getenv("MAKEFUN_LOGIN")
		if owner == "" {
			owner = "octocat"
		}
		f.next++
		q := &repo{Owner: owner, Name: name, ID: f.next, Labels: map[string]bool{}}
		f.repos[owner+"/"+name] = q
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(q.json())
		return
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) >= 5 && p[0] == "api" && p[1] == "v3" && p[2] == "repos" {
		q := f.repos[p[3]+"/"+p[4]]
		if q == nil {
			http.NotFound(w, r)
			return
		}
		if len(p) == 5 {
			json.NewEncoder(w).Encode(q.json())
			return
		}
		if p[5] == "issues" && r.Method == "POST" {
			var x map[string]any
			if json.NewDecoder(r.Body).Decode(&x) != nil {
				http.Error(w, "{\"message\":\"Malformed JSON\"}", 400)
				return
			}
			f.issue++
			x["number"] = f.issue
			x["state"] = "open"
			x["id"] = f.issue
			q.Issues = append(q.Issues, x)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(x)
			return
		}
		if p[5] == "issues" && len(p) > 6 {
			n := p[6]
			for _, x := range q.Issues {
				if fmt.Sprint(x["number"]) == n {
					json.NewEncoder(w).Encode(x)
					return
				}
			}
		}
	}
	if r.URL.Path == "/api/graphql" {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		return
	}
	if r.URL.Path == "/mcp" {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		return
	}
	if len(p) > 1 && strings.HasSuffix(p[1], ".git") {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"message": "repository not found"})
		return
	}
	w.WriteHeader(404)
	json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
}
func (q *repo) json() map[string]any {
	return map[string]any{"id": q.ID, "name": q.Name, "full_name": q.Owner + "/" + q.Name, "owner": map[string]string{"login": q.Owner}, "description": q.Description, "private": false, "default_branch": "main"}
}
func main() {
	root, err := os.MkdirTemp("", "r7-git-data-")
	if err != nil {
		panic(err)
	}
	f := &forge{repos: map[string]*repo{}, gitRoot: root}
	f.seedDemo()
	l, e := net.Listen("tcp", ":0")
	if e != nil {
		panic(e)
	}
	s := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go s.Serve(l)
	fmt.Println(l.Addr())
	env := strings.TrimRight(os.Getenv("MAKEFUN_ENV_URL"), "/")
	if env == "" {
		select {}
	}
	b, _ := json.Marshal(map[string]int{"port": l.Addr().(*net.TCPAddr).Port})
	c := http.Client{Timeout: 10 * time.Second}
	for {
		x, e := c.Post(env+"/register", "application/json", strings.NewReader(string(b)))
		if e == nil {
			x.Body.Close()
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	for {
		x, e := c.Get(env + "/done")
		if e == nil {
			var v map[string]bool
			json.NewDecoder(x.Body).Decode(&v)
			x.Body.Close()
			if v["done"] {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.Close()
}
