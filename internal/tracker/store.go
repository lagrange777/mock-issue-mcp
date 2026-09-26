package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Issue struct {
	Key          string   `json:"key"`
	Project      string   `json:"project"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	Priority     string   `json:"priority"`
	AssigneeID   string   `json:"assigneeId"`
	AssigneeName string   `json:"assigneeName"`
	DueDate      string   `json:"dueDate"`
	BlockedBy    []string `json:"blockedBy"`
}
type Comment struct {
	ID         string `json:"id"`
	IssueKey   string `json:"issueKey"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	Text       string `json:"text"`
	CreatedAt  string `json:"createdAt"`
}
type Dataset struct {
	Projects []Project `json:"projects"`
	Users    []User    `json:"users"`
	Issues   []Issue   `json:"issues"`
	Comments []Comment `json:"comments"`
}
type Search struct {
	Project    string `json:"project,omitempty"`
	Status     string `json:"status,omitempty"`
	Priority   string `json:"priority,omitempty"`
	AssigneeID string `json:"assigneeId,omitempty"`
	Query      string `json:"query,omitempty"`
	Page       int    `json:"page,omitempty"`
	PageSize   int    `json:"pageSize,omitempty"`
}
type IssuePage struct {
	Items    []Issue `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
	HasMore  bool    `json:"hasMore"`
}
type Comments struct {
	Key   string    `json:"key"`
	Items []Comment `json:"items"`
}

var KeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[1-9][0-9]*$`)
var ErrNotFound = errors.New("Задача не найдена")
var statuses = map[string]bool{"todo": true, "in_progress": true, "blocked": true, "done": true}
var priorities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}

// Load reads a new snapshot on every API request, so fixture edits are live.
func Load(path string) (Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Dataset{}, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 2<<20))
	decoder.DisallowUnknownFields()
	var d Dataset
	if err = decoder.Decode(&d); err != nil {
		return d, err
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return d, errors.New("expected one JSON object")
	}
	users, projects, issues := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, u := range d.Users {
		if u.ID == "" || u.Name == "" || users[u.ID] != "" {
			return d, errors.New("invalid users")
		}
		users[u.ID] = u.Name
	}
	for _, p := range d.Projects {
		if p.ID == "" || p.Name == "" || projects[p.ID] {
			return d, errors.New("invalid projects")
		}
		projects[p.ID] = true
	}
	for i := range d.Issues {
		v := &d.Issues[i]
		if !KeyPattern.MatchString(v.Key) || issues[v.Key] || !projects[v.Project] || !strings.HasPrefix(v.Key, v.Project+"-") || !statuses[v.Status] || !priorities[v.Priority] || users[v.AssigneeID] == "" || strings.TrimSpace(v.Title) == "" {
			return d, fmt.Errorf("invalid issue %q", v.Key)
		}
		if _, err := time.Parse("2006-01-02", v.DueDate); err != nil {
			return d, fmt.Errorf("invalid due date for %s", v.Key)
		}
		v.AssigneeName = users[v.AssigneeID]
		if v.BlockedBy == nil {
			v.BlockedBy = []string{}
		}
		issues[v.Key] = true
	}
	for _, v := range d.Issues {
		seen := map[string]bool{}
		for _, key := range v.BlockedBy {
			if !issues[key] || key == v.Key || seen[key] {
				return d, fmt.Errorf("invalid dependency for %s", v.Key)
			}
			seen[key] = true
		}
	}
	commentIDs := map[string]bool{}
	for i := range d.Comments {
		c := &d.Comments[i]
		if c.ID == "" || commentIDs[c.ID] || !issues[c.IssueKey] || users[c.AuthorID] == "" || strings.TrimSpace(c.Text) == "" {
			return d, errors.New("invalid comment")
		}
		if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
			return d, errors.New("invalid comment date")
		}
		c.AuthorName = users[c.AuthorID]
		commentIDs[c.ID] = true
	}
	sort.Slice(d.Issues, func(i, j int) bool { return d.Issues[i].Key < d.Issues[j].Key })
	sort.Slice(d.Comments, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, d.Comments[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339, d.Comments[j].CreatedAt)
		return a.Before(b)
	})
	return d, nil
}
func (d Dataset) Get(key string) (Issue, error) {
	for _, v := range d.Issues {
		if v.Key == key {
			return v, nil
		}
	}
	return Issue{}, ErrNotFound
}
func (s *Search) Validate() error {
	if s.Status != "" && !statuses[s.Status] {
		return errors.New("Неизвестный статус")
	}
	if s.Priority != "" && !priorities[s.Priority] {
		return errors.New("Неизвестный приоритет")
	}
	if s.Page == 0 {
		s.Page = 1
	}
	if s.PageSize == 0 {
		s.PageSize = 5
	}
	if s.Page < 1 || s.Page > 1000000 || s.PageSize < 1 || s.PageSize > 50 {
		return errors.New("page: 1–1000000; pageSize: 1–50")
	}
	if len(s.Query) > 500 || len(s.Project) > 100 || len(s.AssigneeID) > 100 {
		return errors.New("Слишком длинный фильтр")
	}
	return nil
}
func (d Dataset) Search(s Search) (IssuePage, error) {
	if err := s.Validate(); err != nil {
		return IssuePage{}, err
	}
	items := []Issue{}
	q := strings.ToLower(strings.TrimSpace(s.Query))
	for _, v := range d.Issues {
		if s.Project != "" && v.Project != s.Project || s.Status != "" && v.Status != s.Status || s.Priority != "" && v.Priority != s.Priority || s.AssigneeID != "" && v.AssigneeID != s.AssigneeID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(v.Key+" "+v.Title+" "+v.Description), q) {
			continue
		}
		items = append(items, v)
	}
	start := min((s.Page-1)*s.PageSize, len(items))
	end := min(start+s.PageSize, len(items))
	return IssuePage{Items: items[start:end], Total: len(items), Page: s.Page, PageSize: s.PageSize, HasMore: end < len(items)}, nil
}
func (d Dataset) CommentsFor(key string) (Comments, error) {
	if _, err := d.Get(key); err != nil {
		return Comments{}, err
	}
	result := Comments{Key: key, Items: []Comment{}}
	for _, c := range d.Comments {
		if c.IssueKey == key {
			result.Items = append(result.Items, c)
		}
	}
	return result, nil
}
