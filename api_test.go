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
