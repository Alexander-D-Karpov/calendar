package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func testMCP(t *testing.T) *mcpServer {
	t.Helper()
	docs, err := newSpecDocs()
	if err != nil {
		t.Fatal(err)
	}
	m, err := newMCP(Deps{Logger: slog.New(slog.DiscardHandler)}, docs.json.body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mcpCall(t *testing.T, m *mcpServer, ctx context.Context, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := m.serve(rec, req); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, out
}

func rpcCode(out map[string]any) float64 {
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func TestMCPProtocol(t *testing.T) {
	m := testMCP(t)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		UserID: domain.NewID(), TokenID: domain.NewID(), Scopes: []auth.Scope{auth.ScopeTodosRead},
	})

	code, out := mcpCall(t, m, ctx, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	res, _ := out["result"].(map[string]any)
	if code != http.StatusOK || res["protocolVersion"] != "2025-03-26" || res["capabilities"] == nil {
		t.Fatalf("initialize = %d %v", code, out)
	}
	if code, _ := mcpCall(t, m, ctx, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted {
		t.Fatalf("notification = %d", code)
	}

	_, out = mcpCall(t, m, ctx, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var names []string
	for _, tl := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"get_todo", "list_todo_lists", "list_todos"}) {
		t.Fatalf("visible tools = %v", names)
	}

	_, out = mcpCall(t, m, ctx, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_todo","arguments":{"title":"x"}}}`)
	if res, _ := out["result"].(map[string]any); res["isError"] != true {
		t.Fatalf("missing scope = %v", out)
	}

	cases := map[string]float64{
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`:                                   rpcMethodNotFound,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"missing"}}`: rpcInvalidParams,
		`[{"jsonrpc":"2.0","id":6,"method":"ping"}]`:                                 rpcInvalidRequest,
		`{"jsonrpc":"1.0","id":7,"method":"ping"}`:                                   rpcInvalidRequest,
		`{`: rpcParseError,
	}
	for body, want := range cases {
		if _, out := mcpCall(t, m, ctx, body); rpcCode(out) != want {
			t.Errorf("%s: code = %v, want %v", body, rpcCode(out), want)
		}
	}
}

func TestMCPSchemas(t *testing.T) {
	m := testMCP(t)
	for _, tool := range m.tools {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if tool.InputSchema["type"] != "object" || strings.Contains(s, "$ref") || strings.Contains(s, "nullable") || strings.Contains(s, `"example`) {
			t.Errorf("%s: bad schema %s", tool.Name, s)
		}
		if tool.scope == "" || tool.run == nil || tool.Description == "" {
			t.Errorf("%s: incomplete tool", tool.Name)
		}
	}
	props := m.tools[m.index["update_event"]].InputSchema["properties"].(map[string]any)
	if props["id"] == nil || props["title"] == nil || props["scope"] == nil {
		t.Fatalf("update_event properties = %v", props)
	}
	if got := fmt.Sprint(props["location"].(map[string]any)["type"]); got != "[string null]" {
		t.Fatalf("nullable location type = %s", got)
	}
	checks := m.tools[m.index["create_todo"]].InputSchema["properties"].(map[string]any)["checks"].(map[string]any)
	if checks["items"].(map[string]any)["type"] != "object" {
		t.Fatalf("checks items not resolved: %v", checks)
	}
}
