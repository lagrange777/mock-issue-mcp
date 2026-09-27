// Package mcpserver adapts the tracker HTTP API to MCP tools.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mock-issue-mcp/internal/tracker"
)

type KeyInput struct {
	Key string `json:"key"`
}
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) get(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("Не удалось создать запрос к API")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("API трекера недоступен или истёк тайм-аут")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return tracker.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("API трекера вернул HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dest); err != nil {
		return fmt.Errorf("API трекера вернул некорректные данные")
	}
	return nil
}

func New(apiURL string, reportDirs ...string) *mcp.Server {
	reportsDir := "reports"
	if len(reportDirs) > 0 && strings.TrimSpace(reportDirs[0]) != "" {
		reportsDir = reportDirs[0]
	}
	client := Client{BaseURL: strings.TrimRight(apiURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	server := mcp.NewServer(&mcp.Implementation{Name: "mock-issue-mcp", Version: "1.1.0"}, nil)
	keySchema := map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string", "pattern": tracker.KeyPattern.String(), "description": "Ключ задачи, например DEMO-101"}}, "required": []string{"key"}, "additionalProperties": false}
	mcp.AddTool(server, &mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, Name: "get_issue", Description: "Получить задачу по ключу: описание, статус, приоритет, исполнителя, срок и ключи блокирующих задач. Чтобы выяснить причину блокировки, прочитайте задачи из blockedBy и их комментарии.", InputSchema: keySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in KeyInput) (*mcp.CallToolResult, tracker.Issue, error) {
		var out tracker.Issue
		err := client.get(ctx, "/api/issues/"+url.PathEscape(in.Key), &out)
		return nil, out, err
	})
	properties := map[string]any{
		"project":    map[string]any{"type": "string", "maxLength": 100, "description": "Код проекта: DEMO, OPS или EMPTY"},
		"status":     map[string]any{"type": "string", "enum": []string{"todo", "in_progress", "blocked", "done"}, "description": "Статус задачи"},
		"priority":   map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "critical"}, "description": "Приоритет"},
		"assigneeId": map[string]any{"type": "string", "maxLength": 100, "description": "ID исполнителя, например user-2"},
		"query":      map[string]any{"type": "string", "maxLength": 500, "description": "Поиск по ключу, названию и описанию без учёта регистра"},
		"page":       map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000, "description": "Номер страницы, по умолчанию 1"},
		"pageSize":   map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Размер страницы, по умолчанию 5"},
	}
	mcp.AddTool(server, &mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, Name: "search_issues", Description: "Найти задачи по проекту, статусу, приоритету, исполнителю и тексту. Фильтры объединяются через И. Возвращает страницу, total и hasMore; если hasMore=true, запросите следующую page. Пустой список — нормальный результат.", InputSchema: map[string]any{"type": "object", "properties": properties, "additionalProperties": false}}, func(ctx context.Context, _ *mcp.CallToolRequest, in tracker.Search) (*mcp.CallToolResult, tracker.IssuePage, error) {
		if err := in.Validate(); err != nil {
			return nil, tracker.IssuePage{}, err
		}
		q := url.Values{"page": {strconv.Itoa(in.Page)}, "pageSize": {strconv.Itoa(in.PageSize)}}
		for key, value := range map[string]string{"project": in.Project, "status": in.Status, "priority": in.Priority, "assigneeId": in.AssigneeID, "query": in.Query} {
			if value != "" {
				q.Set(key, value)
			}
		}
		var out tracker.IssuePage
		err := client.get(ctx, "/api/issues?"+q.Encode(), &out)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, Name: "get_issue_comments", Description: "Прочитать комментарии задачи от старых к новым. Используйте для уточнения причин блокировки, договорённостей и следующего действия. Возвращает авторов и даты.", InputSchema: keySchema}, func(ctx context.Context, _ *mcp.CallToolRequest, in KeyInput) (*mcp.CallToolResult, tracker.Comments, error) {
		var out tracker.Comments
		err := client.get(ctx, "/api/issues/"+url.PathEscape(in.Key)+"/comments", &out)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, Name: "summarize_issues", Description: "Преобразовать результат search_issues в детерминированный Markdown-отчёт. Передайте поле items из результата поиска без изменений.", InputSchema: summarizeSchema()}, func(_ context.Context, _ *mcp.CallToolRequest, in SummarizeInput) (*mcp.CallToolResult, IssueReport, error) {
		out, err := summarizeIssues(in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(false)}, Name: "save_issue_report", Description: "Сохранить Markdown из summarize_issues в новый файл каталога reports. Разрешены только простые имена .md; существующий файл с другим содержимым не перезаписывается.", InputSchema: saveSchema()}, func(_ context.Context, _ *mcp.CallToolRequest, in SaveReportInput) (*mcp.CallToolResult, SavedReport, error) {
		out, err := saveIssueReport(reportsDir, in)
		return nil, out, err
	})
	return server
}

func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}
