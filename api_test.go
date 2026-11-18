package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func apiForge() *forge { f := &forge{repos: map[string]*repo{}}; f.seedAPI(); return f }

func TestHealthResponseRemainsAvailable(t *testing.T) {
	f := apiForge()
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("health response = %d %s", w.Code, w.Body)
	}
}

func callJSON(t *testing.T, f *forge, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	w := httptest.NewRecorder()
	f.ServeHTTP(w, req)
	return w
}
func TestRESTIssueCreateAndGet(t *testing.T) {
	f := apiForge()
	w := callJSON(t, f, "POST", "/api/v3/repos/octocat/hello-world/issues", map[string]any{"title": "API issue", "body": "body"})
	if w.Code != 201 {
		t.Fatalf("create status %d: %s", w.Code, w.Body)
	}
	var x item
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	got := callJSON(t, f, "GET", "/api/v3/repos/octocat/hello-world/issues/"+itoa(x.Number), nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "API issue") {
		t.Fatalf("get = %d %s", got.Code, got.Body)
	}
}
func TestRESTPullCreateAndList(t *testing.T) {
	f := apiForge()
	w := callJSON(t, f, "POST", "/api/v3/repos/octocat/hello-world/pulls", map[string]any{"title": "API PR", "head": "feature", "base": "main"})
	if w.Code != 201 {
		t.Fatalf("create status %d: %s", w.Code, w.Body)
	}
	got := callJSON(t, f, "GET", "/api/v3/repos/octocat/hello-world/pulls", nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "API PR") {
		t.Fatalf("list = %d %s", got.Code, got.Body)
	}
}
func TestGraphQLRepositoryAndMCPCall(t *testing.T) {
	f := apiForge()
	gql := callJSON(t, f, "POST", "/graphql", map[string]any{"query": "{ repository(owner: \"octocat\", name: \"hello-world\") { name owner { login } issues { nodes { number title state } } pullRequests { nodes { number title state } } } }"})
	if gql.Code != 200 || !strings.Contains(gql.Body.String(), "hello-world") {
		t.Fatalf("graphql = %d %s", gql.Code, gql.Body)
	}
	mcp := callJSON(t, f, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "create_issue", "arguments": map[string]any{"owner": "octocat", "repo": "hello-world", "title": "MCP issue"}}})
	if mcp.Code != 200 || !strings.Contains(mcp.Body.String(), "MCP issue") {
		t.Fatalf("mcp = %d %s", mcp.Code, mcp.Body)
	}
}
func itoa(n int) string { return fmt.Sprint(n) }

func TestMergeReportsUnsupportedBuildAndHonorsReviewProtection(t *testing.T) {
	f := testForge(t)
	f.repos["octocat/hello-world"] = &repo{Owner: "octocat", Name: "hello-world", PRs: []*item{{Number: 7, Title: "test", Head: "feature", Base: "main", State: "open"}}}
	base := "/api/v3/repos/octocat/hello-world"
	callJSON(t, f, "PUT", base+"/branches/main/protection", map[string]any{"required_approving_reviews": 1, "require_pull_request": true})
	blocked := callJSON(t, f, "POST", base+"/pulls/7/merge", map[string]any{})
	if blocked.Code != 403 {
		t.Fatalf("unreviewed merge = %d %s", blocked.Code, blocked.Body)
	}
	rv := callJSON(t, f, "POST", base+"/pulls/7/reviews", map[string]any{"reviewer": "alice", "event": "approve", "body": "looks good"})
	if rv.Code != 201 {
		t.Fatalf("review = %d %s", rv.Code, rv.Body)
	}
	allowed := callJSON(t, f, "POST", base+"/pulls/7/merge", map[string]any{})
	if allowed.Code != 422 || !strings.Contains(allowed.Body.String(), "not supported") {
		t.Fatalf("unsupported merge = %d %s", allowed.Code, allowed.Body)
	}
}

func TestReceivePackCASOldSHA(t *testing.T) {
	f := testForge(t)
	old := "0000000000000000000000000000000000000000"
	body := append(EncodePktLine([]byte(old+" "+old+" refs/heads/main\x00report-status")), []byte("0000")...)
	stale, ref := f.staleReceivePack("", body)
	if !stale || ref != "refs/heads/main" {
		t.Fatalf("stale check = %v %q", stale, ref)
	}
	current, _ := f.objects.ReadRef("refs/heads/main")
	match := append(EncodePktLine([]byte(current+" "+old+" refs/heads/main\x00report-status")), []byte("0000")...)
	stale, ref = f.staleReceivePack("", match)
	if stale {
		t.Fatalf("matching CAS rejected at %s", ref)
	}
}
