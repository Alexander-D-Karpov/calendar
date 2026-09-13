package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const (
	MCPPath        = "/api/mcp"
	maxSchemaDepth = 32

	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

const mcpInstructions = "Manages the user's calendar events and todos. Call get_account first to learn the account timezone. " +
	"Times are RFC 3339; a local time without an offset is read in the event timezone. All-day events use dates with an exclusive end. " +
	"Occurrences of a recurring series share the series id; pass their instance value and a scope of this, following or all to change or delete them."

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type toolHints struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolFunc func(ctx context.Context, owner domain.ID, args json.RawMessage) (any, error)

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations toolHints      `json:"annotations"`
	scope       auth.Scope
	run         toolFunc
}

type mcpServer struct {
	logger *slog.Logger
	tools  []mcpTool
	index  map[string]int
}

func newMCP(d Deps, specJSON []byte) (*mcpServer, error) {
	schemas, err := newSchemaSet(specJSON)
	if err != nil {
		return nil, err
	}
	m := &mcpServer{logger: d.Logger, tools: mcpTools(d, schemas), index: map[string]int{}}
	for i, t := range m.tools {
		if _, dup := m.index[t.Name]; dup {
			return nil, fmt.Errorf("mcp: duplicate tool %s", t.Name)
		}
		m.index[t.Name] = i
	}
	return m, nil
}

func requireBearer(fail auth.Fail) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := auth.BearerToken(r); !ok {
				fail(w, r, fmt.Errorf("%w: the MCP endpoint needs an API token in the Authorization header", domain.ErrUnauthorized))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (m *mcpServer) serve(w http.ResponseWriter, r *http.Request) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != mediaJSON {
		return &httpx.StatusError{Code: http.StatusUnsupportedMediaType, Msg: "content type must be application/json"}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		code := rpcInvalidRequest
		if !json.Valid(body) {
			code = rpcParseError
		}
		writeRPC(w, rpcResponse{Error: &rpcError{Code: code, Message: "body must be a single JSON-RPC 2.0 object"}})
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPC(w, rpcResponse{ID: req.ID, Error: &rpcError{Code: rpcInvalidRequest, Message: "invalid JSON-RPC 2.0 request"}})
		return nil
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	resp := rpcResponse{ID: req.ID}
	resp.Result, resp.Error = m.dispatch(r.Context(), req)
	writeRPC(w, resp)
	return nil
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	resp.JSONRPC = "2.0"
	writeJSON(w, http.StatusOK, resp)
}

func (m *mcpServer) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "calendar", "title": "Calendar", "version": buildinfo.Get().Version},
			"instructions":    mcpInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": m.visible(ctx)}, nil
	case "tools/call":
		return m.call(ctx, req.Params)
	}
	return nil, &rpcError{Code: rpcMethodNotFound, Message: "method not found: " + req.Method}
}

func (m *mcpServer) visible(ctx context.Context) []mcpTool {
	p := auth.PrincipalFrom(ctx)
	out := make([]mcpTool, 0, len(m.tools))
	for _, t := range m.tools {
		if p.Can(t.scope) {
			out = append(out, t)
		}
	}
	return out
}

func (m *mcpServer) call(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Name == "" {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "params must contain a tool name"}
	}
	i, ok := m.index[p.Name]
	if !ok {
		return nil, &rpcError{Code: rpcInvalidParams, Message: "unknown tool " + p.Name}
	}
	t := m.tools[i]
	pr := auth.PrincipalFrom(ctx)
	if !pr.Can(t.scope) {
		return toolError("the token lacks scope " + string(t.scope)), nil
	}
	args := p.Arguments
	if len(bytes.TrimSpace(args)) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	out, err := t.run(ctx, pr.UserID, args)
	if err != nil {
		return m.failure(ctx, t.Name, err), nil
	}
	res, err := toolResult(out)
	if err != nil {
		return m.failure(ctx, t.Name, err), nil
	}
	return res, nil
}

func toolResult(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"content": []toolContent{{Type: "text", Text: string(b)}}, "isError": false}
	if bytes.HasPrefix(b, []byte("{")) {
		res["structuredContent"] = json.RawMessage(b)
	}
	return res, nil
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []toolContent{{Type: "text", Text: msg}}, "isError": true}
}

func (m *mcpServer) failure(ctx context.Context, tool string, err error) map[string]any {
	status := httpx.StatusFor(err)
	if status >= 500 {
		m.logger.LogAttrs(ctx, slog.LevelError, "mcp tool failed", slog.String("tool", tool), slog.Any("err", err))
		return toolError("internal error, the request was logged")
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		parts := make([]string, len(ve.Fields))
		for i, f := range ve.Fields {
			parts[i] = f.Field + " " + f.Message
		}
		return toolError("invalid input: " + strings.Join(parts, "; "))
	}
	msg := http.StatusText(status)
	if d := publicDetail(err); d != "" {
		msg += ": " + d
	}
	return toolError(msg)
}

func decodeArgs(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	return nil
}

func splitArgs(raw json.RawMessage, keys ...string) (json.RawMessage, json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, decodeError(err)
	}
	picked := make(map[string]json.RawMessage, len(keys))
	for _, k := range keys {
		if v, ok := all[k]; ok {
			picked[k] = v
			delete(all, k)
		}
	}
	head, err := json.Marshal(picked)
	if err != nil {
		return nil, nil, err
	}
	rest, err := json.Marshal(all)
	return head, rest, err
}

func argID(field, s string) (domain.ID, error) {
	id, err := domain.ParseID(strings.TrimSpace(s))
	if err != nil {
		var v domain.ValidationError
		v.Add(field, "must be an id returned by another tool")
		return domain.NilID, v.Err()
	}
	return id, nil
}

type schemaSet struct {
	schemas map[string]any
}

func newSchemaSet(specJSON []byte) (schemaSet, error) {
	var root struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(specJSON, &root); err != nil {
		return schemaSet{}, fmt.Errorf("mcp: read spec: %w", err)
	}
	return schemaSet{schemas: root.Components.Schemas}, nil
}

func (s schemaSet) input(name string, extra map[string]any, required ...string) map[string]any {
	base, _ := s.resolve(map[string]any{"$ref": "#/components/schemas/" + name}, 0).(map[string]any)
	props := map[string]any{}
	if p, ok := base["properties"].(map[string]any); ok {
		maps.Copy(props, p)
	}
	maps.Copy(props, extra)
	return object(props, required...)
}

func (s schemaSet) resolve(v any, depth int) any {
	if depth > maxSchemaDepth {
		return map[string]any{}
	}
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = s.resolve(e, depth+1)
		}
		return out
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			return s.resolve(s.schemas[strings.TrimPrefix(ref, "#/components/schemas/")], depth+1)
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			switch k {
			case "example", "examples", "nullable":
				continue
			}
			out[k] = s.resolve(e, depth+1)
		}
		if x["nullable"] == true {
			if t, ok := out["type"].(string); ok {
				out["type"] = []any{t, "null"}
			}
		}
		return out
	}
	return v
}
