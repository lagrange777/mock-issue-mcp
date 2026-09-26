// mcp-check is a smoke-test client for the running container.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	endpoint := flag.String("url", "http://127.0.0.1:8090/mcp", "MCP endpoint")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-issue-smoke-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *endpoint, DisableStandaloneSSE: true}, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()
	info := session.InitializeResult()
	fmt.Printf("Connected: %s %s (MCP %s)\n", info.ServerInfo.Name, info.ServerInfo.Version, info.ProtocolVersion)
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, tool := range list.Tools {
		fmt.Printf("Tool: %s — %s\n", tool.Name, tool.Description)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"get_issue", map[string]any{"key": "DEMO-101"}},
		{"get_issue", map[string]any{"key": "DEMO-100"}},
		{"get_issue_comments", map[string]any{"key": "DEMO-100"}},
		{"search_issues", map[string]any{"project": "DEMO", "status": "blocked", "pageSize": 2}},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil {
			log.Fatal(err)
		}
		if result.IsError {
			log.Fatalf("%s returned an error: %+v", call.name, result)
		}
		data, err := json.MarshalIndent(result.StructuredContent, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\n%s: %s\n", call.name, data)
	}
}
