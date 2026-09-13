package api

import (
	"log/slog"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/exporter"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
	"github.com/Alexander-D-Karpov/calendar/internal/search"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/subscription"
)

const Prefix = "/api/v1"

type Deps struct {
	Settings    *service.Settings
	Calendars   *service.Calendars
	Events      *service.Events
	TodoLists   *service.TodoLists
	Todos       *service.Todos
	Import      *importer.Service
	Export      *exporter.Service
	Subs        *subscription.Service
	Dedup       *dedup.Service
	Store       TrashStore
	Search      *search.Service
	Shares      *service.Shares
	Changes     ChangeStore
	Sync        *gsync.Engine
	BaseURL     string
	Idempotency IdempotencyStore
	Guard       *auth.Guard
	Limiter     *ratelimit.Limiter
	Logger      *slog.Logger
}

type route struct {
	method  string
	path    string
	scope   auth.Scope
	handler func(Deps) apiFunc
}

var routes = []route{
	{http.MethodGet, "/me", auth.ScopeAccountRead, getMe},
	{http.MethodPatch, "/me", auth.ScopeAccountWrite, updateMe},
	{http.MethodGet, "/calendars", auth.ScopeCalendarsRead, listCalendars},
	{http.MethodPost, "/calendars", auth.ScopeCalendarsWrite, createCalendar},
	{http.MethodGet, "/calendars/{id}", auth.ScopeCalendarsRead, getCalendar},
	{http.MethodPatch, "/calendars/{id}", auth.ScopeCalendarsWrite, updateCalendar},
	{http.MethodDelete, "/calendars/{id}", auth.ScopeCalendarsWrite, deleteCalendar},
	{http.MethodGet, "/events", auth.ScopeCalendarsRead, listEvents},
	{http.MethodPost, "/events", auth.ScopeCalendarsWrite, createEvent},
	{http.MethodGet, "/events/{id}", auth.ScopeCalendarsRead, getEvent},
	{http.MethodPatch, "/events/{id}", auth.ScopeCalendarsWrite, updateEvent},
	{http.MethodDelete, "/events/{id}", auth.ScopeCalendarsWrite, deleteEvent},
	{http.MethodGet, "/events/{id}/instances", auth.ScopeCalendarsRead, listEventInstances},
	{http.MethodGet, "/todo-lists", auth.ScopeTodosRead, listTodoLists},
	{http.MethodPost, "/todo-lists", auth.ScopeTodosWrite, createTodoList},
	{http.MethodGet, "/todo-lists/{id}", auth.ScopeTodosRead, getTodoList},
	{http.MethodPatch, "/todo-lists/{id}", auth.ScopeTodosWrite, updateTodoList},
	{http.MethodDelete, "/todo-lists/{id}", auth.ScopeTodosWrite, deleteTodoList},
	{http.MethodGet, "/todos", auth.ScopeTodosRead, listTodos},
	{http.MethodPost, "/todos", auth.ScopeTodosWrite, createTodo},
	{http.MethodGet, "/todos/{id}", auth.ScopeTodosRead, getTodo},
	{http.MethodPatch, "/todos/{id}", auth.ScopeTodosWrite, updateTodo},
	{http.MethodDelete, "/todos/{id}", auth.ScopeTodosWrite, deleteTodo},
	{http.MethodPost, "/todos/{id}/complete", auth.ScopeTodosWrite, completeTodo},
	{http.MethodPost, "/todos/{id}/reopen", auth.ScopeTodosWrite, reopenTodo},
	{http.MethodPost, "/todos/{id}/move", auth.ScopeTodosWrite, moveTodo},
	{http.MethodGet, "/todos/{id}/checks", auth.ScopeTodosRead, listChecks},
	{http.MethodPost, "/todos/{id}/checks", auth.ScopeTodosWrite, createCheck},
	{http.MethodPut, "/todos/{id}/checks", auth.ScopeTodosWrite, reorderChecks},
	{http.MethodPatch, "/todos/{id}/checks/{cid}", auth.ScopeTodosWrite, updateCheck},
	{http.MethodDelete, "/todos/{id}/checks/{cid}", auth.ScopeTodosWrite, deleteCheck},
	{http.MethodGet, "/imports", auth.ScopeImportsWrite, listImports},
	{http.MethodPost, "/imports", auth.ScopeImportsWrite, createImport},
	{http.MethodGet, "/imports/{id}", auth.ScopeImportsWrite, getImport},
	{http.MethodPost, "/imports/{id}/commit", auth.ScopeImportsWrite, commitImport},
	{http.MethodGet, "/exports/ics", auth.ScopeExportsRead, exportICS},
	{http.MethodGet, "/exports/csv", auth.ScopeExportsRead, exportCSV},
	{http.MethodGet, "/exports/account.yaml", auth.ScopeExportsRead, exportYAML},
	{http.MethodGet, "/subscriptions", auth.ScopeCalendarsRead, listSubscriptions},
	{http.MethodPost, "/subscriptions", auth.ScopeCalendarsWrite, createSubscription},
	{http.MethodPost, "/subscriptions/{id}/refresh", auth.ScopeCalendarsWrite, refreshSubscription},
	{http.MethodDelete, "/subscriptions/{id}", auth.ScopeCalendarsWrite, deleteSubscription},
	{http.MethodGet, "/duplicates", auth.ScopeCalendarsRead, listDuplicates},
	{http.MethodPost, "/duplicates/scan", auth.ScopeCalendarsWrite, scanDuplicates},
	{http.MethodPost, "/duplicates/{id}/resolve", auth.ScopeCalendarsWrite, resolveDuplicate},
	{http.MethodGet, "/trash", auth.ScopeCalendarsRead, listTrash},
	{http.MethodPost, "/trash/{entity}/{id}/restore", auth.ScopeCalendarsWrite, restoreTrash},
	{http.MethodDelete, "/trash/{entity}/{id}", auth.ScopeCalendarsWrite, purgeTrash},
	{http.MethodGet, "/search", auth.ScopeCalendarsRead, runSearch},
	{http.MethodGet, "/shares", auth.ScopeSharesRead, listShares},
	{http.MethodPost, "/shares", auth.ScopeSharesWrite, createShare},
	{http.MethodGet, "/shares/{id}", auth.ScopeSharesRead, getShare},
	{http.MethodPatch, "/shares/{id}", auth.ScopeSharesWrite, updateShare},
	{http.MethodPost, "/shares/{id}/regenerate", auth.ScopeSharesWrite, regenerateShare},
	{http.MethodDelete, "/shares/{id}", auth.ScopeSharesWrite, revokeShare},
	{http.MethodGet, "/changes", auth.ScopeCalendarsRead, listChanges},
	{http.MethodGet, "/sync/status", auth.ScopeSyncWrite, getSyncStatus},
	{http.MethodPost, "/sync/run", auth.ScopeSyncWrite, runSync},
}

// Creating collections is what a retry after a dropped connection duplicates;
// the rest are either idempotent already or act on a named resource.
var idempotent = map[string]bool{
	"/events":     true,
	"/todos":      true,
	"/calendars":  true,
	"/todo-lists": true,
	"/imports":    true,
	"/shares":     true,
}

func Routes(mux *http.ServeMux, d Deps) error {
	fail := Fail(d.Logger)
	limit := ratelimit.Middleware(d.Limiter, auth.ByPrincipal, fail)
	docs, err := newSpecDocs()
	if err != nil {
		return err
	}
	mcp, err := newMCP(d, docs.json.body)
	if err != nil {
		return err
	}
	idem := idempotency(d.Idempotency, fail)
	allowed := map[string][]string{}
	for _, rt := range routes {
		h := rt.handler(d).serve(fail)
		if rt.method == http.MethodPost && idempotent[rt.path] {
			h = idem(h)
		}
		h = httpx.Chain(h, d.Guard.API, limit, d.Guard.RequireScope(rt.scope))
		mux.Handle(rt.method+" "+Prefix+rt.path, h)
		allowed[rt.path] = append(allowed[rt.path], rt.method)
	}
	for path, methods := range allowed {
		mux.Handle(Prefix+path, methodNotAllowed(methods, fail))
	}
	mux.Handle("POST "+MCPPath, httpx.Chain(apiFunc(mcp.serve).serve(fail), requireBearer(fail), d.Guard.API, limit))
	mux.Handle(MCPPath, methodNotAllowed([]string{http.MethodPost}, fail))
	mux.Handle("GET /api/openapi.yaml", docs.yaml)
	mux.Handle("GET /api/openapi.json", docs.json)
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, r, domain.ErrNotFound)
	}))
	return nil
}
