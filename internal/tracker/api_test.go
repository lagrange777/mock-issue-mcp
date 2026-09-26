package tracker

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	data, err := os.ReadFile("../../data/seed.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(path)
	call := func(url string, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != want {
			t.Fatalf("%s: %d %s", url, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var issue Issue
	json.Unmarshal(call("/api/issues/DEMO-101", 200), &issue)
	if issue.Status != "blocked" || issue.AssigneeName == "" || len(issue.BlockedBy) != 1 || issue.BlockedBy[0] != "DEMO-100" {
		t.Fatalf("%+v", issue)
	}
	var page IssuePage
	json.Unmarshal(call("/api/issues?project=DEMO&status=blocked&priority=high&pageSize=1", 200), &page)
	if page.Total != 2 || len(page.Items) != 1 || !page.HasMore || page.Items[0].Key != "DEMO-101" {
		t.Fatalf("%+v", page)
	}
	json.Unmarshal(call("/api/issues?project=DEMO&status=blocked&priority=high&pageSize=1&page=2", 200), &page)
	if len(page.Items) != 1 || page.HasMore || page.Items[0].Key != "DEMO-102" {
		t.Fatalf("%+v", page)
	}
	json.Unmarshal(call("/api/issues?project=EMPTY", 200), &page)
	if page.Total != 0 || page.Items == nil {
		t.Fatal("empty project must return []")
	}
	json.Unmarshal(call("/api/issues?assigneeId=user-4&query=%D0%9C%D0%9E%D0%91%D0%98%D0%9B", 200), &page)
	if page.Total != 1 {
		t.Fatal("text/assignee filter failed")
	}
	var comments Comments
	json.Unmarshal(call("/api/issues/DEMO-100/comments", 200), &comments)
	if len(comments.Items) != 2 || comments.Items[0].AuthorName == "" {
		t.Fatal("comments missing")
	}
	for _, url := range []string{"/api/issues/BAD", "/api/issues?status=unknown", "/api/issues?page=0", "/api/issues?page=-1", "/api/issues?pageSize=51", "/api/issues?wat=1", "/api/issues?page=1&page=2"} {
		call(url, 400)
	}
	call("/api/issues/DEMO-999", 404)
	call("/api/issues/DEMO-999/comments", 404)
	var d Dataset
	json.Unmarshal(data, &d)
	d.Issues[1].Title = "Changed live"
	changed, _ := json.Marshal(d)
	os.WriteFile(path, changed, 0600)
	json.Unmarshal(call("/api/issues/DEMO-101", 200), &issue)
	if issue.Title != "Changed live" {
		t.Fatal("dataset not reloaded")
	}
	d.Issues[1].BlockedBy = []string{"DEMO-999"}
	changed, _ = json.Marshal(d)
	os.WriteFile(path, changed, 0600)
	call("/healthz", 503)
	call("/api/issues", 503)
}
