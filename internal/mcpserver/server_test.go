package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mock-issue-mcp/internal/tracker"
)

func TestToolsOverHTTP(t *testing.T) {
	var apiCalls atomic.Int32
	apiHandler := tracker.NewHandler("../../data/seed.json")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { apiCalls.Add(1); apiHandler.ServeHTTP(w, r) }))
	defer api.Close()
	reportsDir := t.TempDir()
	remote := httptest.NewServer(Handler(New(api.URL, reportsDir)))
	defer remote.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: remote.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 5 {
		t.Fatalf("tools: %d", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatalf("incomplete tool: %+v", tool)
		}
		if tool.Name == "save_issue_report" {
			if tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
				t.Fatalf("unsafe save annotations: %+v", tool.Annotations)
			}
		} else if !tool.Annotations.ReadOnlyHint {
			t.Fatalf("unexpected mutating tool: %+v", tool)
		}
	}
	call := func(name string, args map[string]any, wantError bool) string {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != wantError {
			t.Fatalf("%s: %+v", name, result)
		}
		if !wantError && result.StructuredContent == nil {
			t.Fatal("missing structured result")
		}
		if len(result.Content) == 0 {
			t.Fatal("missing text result")
		}
		return result.Content[0].(*mcp.TextContent).Text
	}
	var issue tracker.Issue
	json.Unmarshal([]byte(call("get_issue", map[string]any{"key": "DEMO-101"}, false)), &issue)
	if issue.Status != "blocked" || issue.BlockedBy[0] != "DEMO-100" {
		t.Fatal("wrong issue")
	}
	call("get_issue", map[string]any{"key": issue.BlockedBy[0]}, false)
	comments := call("get_issue_comments", map[string]any{"key": "DEMO-100"}, false)
	if !strings.Contains(comments, "провайдеру") {
		t.Fatal("missing blocker evidence")
	}
	var page tracker.IssuePage
	json.Unmarshal([]byte(call("search_issues", map[string]any{"project": "DEMO", "pageSize": 1}, false)), &page)
	if page.Total != 7 || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("%+v", page)
	}
	searchItems := append([]tracker.Issue(nil), page.Items...)
	call("search_issues", map[string]any{"project": "EMPTY"}, false)
	if apiCalls.Load() != 5 {
		t.Fatalf("tools must call real HTTP API: %d", apiCalls.Load())
	}

	// Pass structured search data through both following tools exactly as an
	// MCP client/LLM pipeline does.
	summaryJSON := call("summarize_issues", map[string]any{"issues": searchItems, "title": "Задачи DEMO"}, false)
	var report IssueReport
	if err := json.Unmarshal([]byte(summaryJSON), &report); err != nil || report.Count != len(searchItems) || report.Markdown == "" {
		t.Fatalf("summary: %s %v", summaryJSON, err)
	}
	savedJSON := call("save_issue_report", map[string]any{"filename": "blocked-demo.md", "content": report.Markdown}, false)
	var saved SavedReport
	if err := json.Unmarshal([]byte(savedJSON), &saved); err != nil || saved.Bytes != len(report.Markdown) || saved.SHA256 == "" {
		t.Fatalf("save: %s %v", savedJSON, err)
	}
	content, err := os.ReadFile(filepath.Join(reportsDir, "blocked-demo.md"))
	if err != nil || string(content) != report.Markdown {
		t.Fatalf("saved transfer mismatch: %v", err)
	}
	call("save_issue_report", map[string]any{"filename": "blocked-demo.md", "content": report.Markdown}, false)
	call("save_issue_report", map[string]any{"filename": "../escape.md", "content": report.Markdown}, true)
	call("get_issue", map[string]any{"key": "DEMO-999"}, true)
	before := apiCalls.Load()
	for _, args := range []map[string]any{{}, {"key": "../bad"}, {"key": "DEMO-101", "extra": true}} {
		call("get_issue", args, true)
	}
	call("search_issues", map[string]any{"status": "bad"}, true)
	call("search_issues", map[string]any{"pageSize": 51}, true)
	if apiCalls.Load() != before {
		t.Fatal("invalid arguments reached API")
	}
	api.Close()
	message := call("get_issue", map[string]any{"key": "DEMO-101"}, true)
	if !strings.Contains(message, "недоступен") {
		t.Fatal("API failure not reported")
	}
}
