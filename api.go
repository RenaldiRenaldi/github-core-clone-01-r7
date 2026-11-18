package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
)

var mcpTools = []map[string]any{
	{"name": "create_repo", "description": "Create a repository", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]string{"type": "string"}}, "required": []string{"name"}}},
	{"name": "get_repo", "description": "Read a repository", "inputSchema": map[string]any{"type": "object"}},
	{"name": "create_issue", "description": "Open an issue", "inputSchema": map[string]any{"type": "object"}},
	{"name": "list_issues", "description": "List issues", "inputSchema": map[string]any{"type": "object"}},
	{"name": "create_pull_request", "description": "Open a pull request", "inputSchema": map[string]any{"type": "object"}},
	{"name": "merge_pull_request", "description": "Merge a pull request after branch protection checks", "inputSchema": map[string]any{"type": "object"}},
	{"name": "review_pull_request", "description": "Review a pull request", "inputSchema": map[string]any{"type": "object"}},
	{"name": "get_file_contents", "description": "Read file contents", "inputSchema": map[string]any{"type": "object"}},
}

func httptestRequest(method, path string, body []byte) *http.Request {
	// Keep API fixtures and live handlers on the same request construction path.
	return httptest.NewRequest(method, path, bytes.NewReader(body))
}
func httptestRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
