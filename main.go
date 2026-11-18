package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type item struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	State   string   `json:"state"`
	Labels  []string `json:"labels,omitempty"`
	Head    string   `json:"head,omitempty"`
	Base    string   `json:"base,omitempty"`
	Reviews []review `json:"reviews,omitempty"`
}
type review struct {
	Reviewer string `json:"reviewer"`
	State    string `json:"state"`
	Body     string `json:"body"`
}
type protection struct {
	RequiredApprovingReviews int  `json:"required_approving_reviews"`
	RequirePullRequest       bool `json:"require_pull_request"`
}
type repo struct {
	Owner       string  `json:"-"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	ID          int     `json:"id"`
	Issues      []*item `json:"-"`
	PRs         []*item `json:"-"`
}

func (q *repo) json() map[string]any {
	return map[string]any{"id": q.ID, "name": q.Name, "full_name": q.Owner + "/" + q.Name, "owner": map[string]string{"login": q.Owner}, "description": q.Description, "private": false, "default_branch": "main"}
}

type forge struct {
	mu              sync.RWMutex
	repos           map[string]*repo
	next, issue, pr int
	gitRoot         string
	objects         *objectStore
	reviews         map[string][]review
	protections     map[string]protection
}

func (f *forge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.serveGit(w, r) {
		return
	}
	w.Header().Set("X-GitHub-Request-Id", "R7-FORGE")
	if r.URL.Path == "/healthz" {
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/export" {
		f.exportState(w)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
		tok := os.Getenv("MAKEFUN_SHOP_TOKEN")
		if tok != "" && r.Header.Get("Authorization") != "Bearer "+tok && r.Header.Get("Authorization") != "token "+tok {
			writeJSON(w, 401, map[string]string{"message": "Bad credentials"})
			return
		}
	}
	switch r.URL.Path {
	case "/":
		f.ui(w)
	case "/api/v3/", "/api/v3":
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		writeJSON(w, 200, map[string]string{"current_user_url": "/api/v3/user"})
	case "/api/v3/meta":
		writeJSON(w, 200, map[string]string{"installed_version": "r7-go"})
	case "/api/v3/user":
		login := os.Getenv("MAKEFUN_LOGIN")
		if login == "" {
			login = "octocat"
		}
		writeJSON(w, 200, map[string]any{"login": login, "id": 1, "name": "Demo User"})
	case "/api/v3/user/repos":
		f.userRepos(w, r)
	case "/graphql", "/api/graphql":
		f.graphql(w, r)
	case "/mcp":
		f.mcp(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/v3/repos/") {
			f.repoAPI(w, r)
			return
		}
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
	}
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func decode(r *http.Request) map[string]any {
	m := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}
func (f *forge) userRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		in := decode(r)
		owner := os.Getenv("MAKEFUN_LOGIN")
		if owner == "" {
			owner = "octocat"
		}
		name, _ := in["name"].(string)
		if name == "" {
			writeJSON(w, 422, map[string]string{"message": "name is required"})
			return
		}
		f.mu.Lock()
		f.next++
		q := &repo{Owner: owner, Name: name, Description: stringVal(in["description"]), ID: f.next}
		f.repos[owner+"/"+name] = q
		f.mu.Unlock()
		writeJSON(w, 201, q.json())
		return
	}
	f.mu.RLock()
	out := []map[string]any{}
	for _, q := range f.repos {
		out = append(out, q.json())
	}
	f.mu.RUnlock()
	writeJSON(w, 200, out)
}
func (f *forge) repoAPI(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 5 {
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
		return
	}
	key := p[3] + "/" + p[4]
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.repos[key]
	if q == nil {
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
		return
	}
	if f.specialRepoAPI(w, r, q, p) {
		return
	}
	if len(p) == 5 {
		if r.Method == "GET" {
			writeJSON(w, 200, q.json())
		} else {
			writeJSON(w, 405, map[string]string{"message": "Method not allowed"})
		}
		return
	}
	if len(p) < 6 {
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
		return
	}
	collection := p[5]
	if collection != "issues" && collection != "pulls" {
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
		return
	}
	isPR := collection == "pulls"
	var list *[]*item = &q.Issues
	if isPR {
		list = &q.PRs
	}
	if len(p) == 6 {
		if r.Method == "POST" {
			in := decode(r)
			f.issue++
			n := f.issue
			if isPR {
				f.pr++
				n = f.pr
			}
			x := &item{Number: n, Title: stringVal(in["title"]), Body: stringVal(in["body"]), State: "open", Head: stringVal(in["head"]), Base: stringVal(in["base"])}
			if labels, ok := in["labels"].([]any); ok {
				for _, v := range labels {
					if s, ok := v.(string); ok {
						x.Labels = append(x.Labels, s)
					}
				}
			}
			*list = append(*list, x)
			writeJSON(w, 201, x)
			return
		}
		if r.Method == "GET" {
			writeJSON(w, 200, *list)
			return
		}
		writeJSON(w, 405, map[string]string{"message": "Method not allowed"})
		return
	}
	n := 0
	fmt.Sscan(p[6], &n)
	for _, x := range *list {
		if x.Number == n {
			if isPR {
				if r.Method == "GET" {
					writeJSON(w, 200, x)
					return
				}
				writeJSON(w, 405, map[string]string{"message": "Method not allowed"})
				return
			}
			if r.Method == "PATCH" || r.Method == "PUT" {
				in := decode(r)
				if v, ok := in["title"].(string); ok {
					x.Title = v
				}
				if v, ok := in["body"].(string); ok {
					x.Body = v
				}
				if v, ok := in["state"].(string); ok {
					x.State = v
				}
				if vals, ok := in["labels"].([]any); ok {
					x.Labels = nil
					for _, v := range vals {
						if s, ok := v.(string); ok {
							x.Labels = append(x.Labels, s)
						}
					}
				}
				writeJSON(w, 200, x)
				return
			}
			if r.Method == "GET" {
				writeJSON(w, 200, x)
				return
			}
			if r.Method == "POST" && len(p) > 7 && p[7] == "comments" {
				in := decode(r)
				writeJSON(w, 201, map[string]any{"id": n, "body": in["body"]})
				return
			}
			writeJSON(w, 405, map[string]string{"message": "Method not allowed"})
			return
		}
	}
	writeJSON(w, 404, map[string]string{"message": "Not Found"})
}
func stringVal(v any) string { s, _ := v.(string); return s }
func (f *forge) graphql(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, 405, map[string]string{"message": "POST required"})
		return
	}
	in := decode(r)
	query := stringVal(in["query"])
	f.mu.RLock()
	defer f.mu.RUnlock()
	data := map[string]any{}
	if strings.Contains(query, "viewer") {
		login := os.Getenv("MAKEFUN_LOGIN")
		if login == "" {
			login = "octocat"
		}
		data["viewer"] = map[string]string{"login": login}
	}
	if strings.Contains(query, "repository") {
		owner, name := "octocat", "hello-world"
		if vars, ok := in["variables"].(map[string]any); ok {
			if v := stringVal(vars["owner"]); v != "" {
				owner = v
			}
			if v := stringVal(vars["name"]); v != "" {
				name = v
			}
		}
		q := f.repos[owner+"/"+name]
		if q != nil {
			is := []map[string]any{}
			for _, x := range q.Issues {
				is = append(is, map[string]any{"number": x.Number, "title": x.Title, "state": strings.ToUpper(x.State)})
			}
			ps := []map[string]any{}
			for _, x := range q.PRs {
				ps = append(ps, map[string]any{"number": x.Number, "title": x.Title, "state": strings.ToUpper(x.State)})
			}
			data["repository"] = map[string]any{"name": q.Name, "owner": map[string]string{"login": q.Owner}, "issues": map[string]any{"nodes": is}, "pullRequests": map[string]any{"nodes": ps}}
		} else {
			data["repository"] = nil
		}
	}
	writeJSON(w, 200, map[string]any{"data": data})
}
func (f *forge) mcp(w http.ResponseWriter, r *http.Request) {
	in := decode(r)
	id := in["id"]
	method := stringVal(in["method"])
	params, _ := in["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	result := any(nil)
	switch method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-11-25", "serverInfo": map[string]string{"name": "Go Forge", "version": "r7"}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "tools/list":
		result = map[string]any{"tools": mcpTools}
	case "tools/call":
		name := stringVal(params["name"])
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		var payload any
		var status int = 200
		switch name {
		case "create_repo":
			b, _ := json.Marshal(args)
			req := httptestRequest("POST", "/api/v3/user/repos", b)
			rec := httptestRecorder()
			f.userRepos(rec, req)
			status = rec.Code
			_ = json.Unmarshal(rec.Body.Bytes(), &payload)
		case "get_repo", "create_issue", "list_issues", "create_pull_request":
			owner, repoName := stringVal(args["owner"]), stringVal(args["repo"])
			path := "/api/v3/repos/" + owner + "/" + repoName
			meth := "GET"
			if name == "create_issue" {
				path += "/issues"
				meth = "POST"
			} else if name == "list_issues" {
				path += "/issues"
			} else if name == "create_pull_request" {
				path += "/pulls"
				meth = "POST"
			}
			b, _ := json.Marshal(args)
			req := httptestRequest(meth, path, b)
			rec := httptestRecorder()
			f.repoAPI(rec, req)
			status = rec.Code
			if status == 200 || status == 201 {
				_ = json.Unmarshal(rec.Body.Bytes(), &payload)
			} else {
				payload = map[string]string{"message": "request failed"}
			}
		case "merge_pull_request", "review_pull_request", "get_file_contents":
			status = 501
			payload = map[string]string{"message": "not implemented in this API step"}
		default:
			status = 400
			payload = map[string]string{"message": "unknown tool"}
		}
		content, _ := json.Marshal(payload)
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(content)}}, "isError": status >= 400}
	default:
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		return
	}
	writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func (f *forge) ui(w http.ResponseWriter) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><meta charset=utf-8><title>Go Forge</title><h1>Repositories</h1>")
	for _, q := range f.repos {
		fmt.Fprintf(w, "<section><h2>%s/%s</h2><p>%s</p><h3>Issues</h3><ul>", q.Owner, q.Name, q.Description)
		for _, x := range q.Issues {
			fmt.Fprintf(w, "<li>#%d %s (%s)</li>", x.Number, x.Title, x.State)
		}
		fmt.Fprint(w, "</ul><h3>Pull requests</h3><ul>")
		for _, x := range q.PRs {
			fmt.Fprintf(w, "<li>#%d %s (%s)</li>", x.Number, x.Title, x.State)
		}
		fmt.Fprint(w, "</ul></section>")
	}
}
func (f *forge) seedAPI() {
	f.next = 1
	f.issue = 1
	f.pr = 1
	f.repos["octocat/hello-world"] = &repo{Owner: "octocat", Name: "hello-world", Description: "A tiny demo repository.", ID: 1, Issues: []*item{{Number: 1, Title: "Welcome", Body: "Seed issue", State: "open", Labels: []string{"demo"}}}, PRs: []*item{{Number: 1, Title: "Add greeting", Body: "Seed pull request", State: "open", Head: "feature", Base: "main"}}}
	f.repos["octocat/notes"] = &repo{Owner: "octocat", Name: "notes", Description: "Demo notes.", ID: 2}
}
func main() {
	root, err := os.MkdirTemp("", "r7-git-data-")
	if err != nil {
		panic(err)
	}
	f := &forge{repos: map[string]*repo{}, gitRoot: root}
	f.seedAPI()
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
