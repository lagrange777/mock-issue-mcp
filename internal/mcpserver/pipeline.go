package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"mock-issue-mcp/internal/tracker"
)

const maxReportBytes = 256 << 10

var reportNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.md$`)

type SummarizeInput struct {
	Issues []tracker.Issue `json:"issues"`
	Title  string          `json:"title,omitempty"`
}

type IssueReport struct {
	Title     string   `json:"title"`
	Markdown  string   `json:"markdown"`
	IssueKeys []string `json:"issueKeys"`
	Count     int      `json:"count"`
}

type SaveReportInput struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type SavedReport struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
	Existing bool   `json:"existing"`
}

func boolPointer(value bool) *bool { return &value }

func summarizeSchema() map[string]any {
	issue := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":          map[string]any{"type": "string"},
			"project":      map[string]any{"type": "string"},
			"title":        map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
			"priority":     map[string]any{"type": "string"},
			"assigneeId":   map[string]any{"type": "string"},
			"assigneeName": map[string]any{"type": "string"},
			"dueDate":      map[string]any{"type": "string"},
			"blockedBy":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []string{"key", "project", "title", "description", "status", "priority", "assigneeId", "assigneeName", "dueDate", "blockedBy"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"issues": map[string]any{"type": "array", "items": issue, "minItems": 1, "maxItems": 50, "description": "Поле items из результата search_issues"},
			"title":  map[string]any{"type": "string", "maxLength": 200, "description": "Необязательный заголовок отчёта"},
		},
		"required":             []string{"issues"},
		"additionalProperties": false,
	}
}

func saveSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filename": map[string]any{"type": "string", "pattern": reportNamePattern.String(), "description": "Имя Markdown-файла без пути, например blocked-demo.md"},
			"content":  map[string]any{"type": "string", "minLength": 1, "maxLength": maxReportBytes, "description": "Поле markdown из результата summarize_issues"},
		},
		"required":             []string{"filename", "content"},
		"additionalProperties": false,
	}
}

func summarizeIssues(in SummarizeInput) (IssueReport, error) {
	if len(in.Issues) == 0 || len(in.Issues) > 50 {
		return IssueReport{}, errors.New("Передайте от 1 до 50 задач")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Отчёт по задачам"
	}
	if utf8.RuneCountInString(title) > 200 {
		return IssueReport{}, errors.New("Заголовок слишком длинный")
	}
	var report strings.Builder
	fmt.Fprintf(&report, "# %s\n\nВсего задач: %d\n", oneLine(title), len(in.Issues))
	keys := make([]string, 0, len(in.Issues))
	seen := map[string]bool{}
	for _, issue := range in.Issues {
		if !tracker.KeyPattern.MatchString(issue.Key) || seen[issue.Key] || strings.TrimSpace(issue.Title) == "" {
			return IssueReport{}, errors.New("Получен некорректный или повторяющийся элемент поиска")
		}
		seen[issue.Key] = true
		keys = append(keys, issue.Key)
		fmt.Fprintf(&report, "\n## %s — %s\n\n- Статус: %s\n- Приоритет: %s\n- Исполнитель: %s\n- Срок: %s\n", issue.Key, oneLine(issue.Title), oneLine(issue.Status), oneLine(issue.Priority), oneLine(issue.AssigneeName), oneLine(issue.DueDate))
		if len(issue.BlockedBy) > 0 {
			fmt.Fprintf(&report, "- Заблокирована: %s\n", strings.Join(issue.BlockedBy, ", "))
		}
		if description := oneLine(issue.Description); description != "" {
			fmt.Fprintf(&report, "\n%s\n", description)
		}
	}
	return IssueReport{Title: title, Markdown: report.String(), IssueKeys: keys, Count: len(keys)}, nil
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func saveIssueReport(root string, in SaveReportInput) (SavedReport, error) {
	filename := strings.TrimSpace(in.Filename)
	if !reportNamePattern.MatchString(filename) || filepath.Base(filename) != filename {
		return SavedReport{}, errors.New("Разрешено только простое имя файла с расширением .md")
	}
	if in.Content == "" || len(in.Content) > maxReportBytes || !utf8.ValidString(in.Content) {
		return SavedReport{}, errors.New("Отчёт должен содержать от 1 байта до 256 КиБ корректного UTF-8")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return SavedReport{}, errors.New("Не удалось подготовить каталог отчётов")
	}
	destination := filepath.Join(root, filename)
	digest := sha256.Sum256([]byte(in.Content))
	result := SavedReport{Path: filepath.Join("reports", filename), SHA256: hex.EncodeToString(digest[:]), Bytes: len(in.Content)}
	if existing, err := os.ReadFile(destination); err == nil {
		if string(existing) != in.Content {
			return SavedReport{}, errors.New("Файл уже существует с другим содержимым")
		}
		result.Existing = true
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return SavedReport{}, errors.New("Не удалось проверить существующий отчёт")
	}
	temporary, err := os.CreateTemp(root, ".report-*.tmp")
	if err != nil {
		return SavedReport{}, errors.New("Не удалось создать временный отчёт")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.WriteString(in.Content)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return SavedReport{}, errors.New("Не удалось записать отчёт")
	}
	if err = os.Link(temporaryPath, destination); err != nil {
		if existing, readErr := os.ReadFile(destination); readErr == nil && string(existing) == in.Content {
			result.Existing = true
			return result, nil
		}
		return SavedReport{}, errors.New("Не удалось сохранить отчёт: файл уже существует или каталог недоступен")
	}
	return result, nil
}
