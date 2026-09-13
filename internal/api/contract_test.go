package api

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	spec "github.com/Alexander-D-Karpov/calendar/api"
	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type specOp struct {
	method string
	path   string
	body   map[string]any
	params []any
}

func (o specOp) key() string {
	return o.method + " " + o.path
}

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	b, err := yamlToJSON(spec.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func loadDoc(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(spec.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func schemaNamed(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("schema %s not found", name)
	}
	return ref.Value
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func resolve(root map[string]any, v any) map[string]any {
	m := obj(v)
	ref := str(m["$ref"])
	if ref == "" {
		return m
	}
	var cur any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = obj(cur)[part]
	}
	return obj(cur)
}

func operations(root map[string]any) []specOp {
	var out []specOp
	for path, item := range obj(root["paths"]) {
		pi := obj(item)
		for _, m := range httpMethods {
			op := obj(pi[m])
			if op == nil {
				continue
			}
			params := append(slices.Clone(arr(pi["parameters"])), arr(op["parameters"])...)
			out = append(out, specOp{method: strings.ToUpper(m), path: path, body: op, params: params})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func TestSpecIsValidOpenAPI(t *testing.T) {
	if err := loadDoc(t).Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSpecMatchesRoutes(t *testing.T) {
	root := loadSpec(t)
	servers := arr(root["servers"])
	if len(servers) != 1 || str(obj(servers[0])["url"]) != Prefix {
		t.Fatalf("servers must be exactly [%s], got %v", Prefix, servers)
	}
	ops := map[string]specOp{}
	for _, op := range operations(root) {
		ops[op.key()] = op
	}
	handled := map[string]bool{}
	for _, rt := range routes {
		key := rt.method + " " + rt.path
		handled[key] = true
		op, ok := ops[key]
		if !ok {
			t.Errorf("route %s is not documented in api/openapi.yaml", key)
			continue
		}
		if got := str(op.body["x-scope"]); got != string(rt.scope) {
			t.Errorf("%s: x-scope = %q, handler requires %q", key, got, rt.scope)
		}
		if !strings.Contains(str(op.body["description"]), "`"+string(rt.scope)+"`") {
			t.Errorf("%s: description must mention scope `%s`", key, rt.scope)
		}
	}
	for key := range ops {
		if !handled[key] {
			t.Errorf("documented operation %s has no handler", key)
		}
	}
	info := str(obj(root["info"])["description"])
	for _, s := range auth.AllScopes {
		if !strings.Contains(info, "`"+string(s)+"`") {
			t.Errorf("info.description must describe scope `%s`", s)
		}
	}
}

func TestSpecOperationsDocumented(t *testing.T) {
	root := loadSpec(t)
	schemes := obj(obj(root["components"])["securitySchemes"])
	tags := map[string]bool{}
	for _, tg := range arr(root["tags"]) {
		tm := obj(tg)
		if str(tm["description"]) == "" {
			t.Errorf("tag %s needs a description", str(tm["name"]))
		}
		tags[str(tm["name"])] = true
	}
	ids := map[string]string{}
	for _, op := range operations(root) {
		key, b := op.key(), op.body
		for _, f := range []string{"summary", "description", "operationId"} {
			if strings.TrimSpace(str(b[f])) == "" {
				t.Errorf("%s: missing %s", key, f)
			}
		}
		id := str(b["operationId"])
		if prev, dup := ids[id]; dup {
			t.Errorf("%s: operationId %q already used by %s", key, id, prev)
		}
		ids[id] = key
		if len(arr(b["tags"])) == 0 {
			t.Errorf("%s: needs at least one tag", key)
		}
		for _, tg := range arr(b["tags"]) {
			if !tags[str(tg)] {
				t.Errorf("%s: tag %q is not declared at the top level", key, str(tg))
			}
		}
		sec := arr(b["security"])
		if len(sec) == 0 {
			t.Errorf("%s: needs a security requirement", key)
		}
		for _, req := range sec {
			for name := range obj(req) {
				if schemes[name] == nil {
					t.Errorf("%s: unknown security scheme %q", key, name)
				}
			}
		}
		checkParams(t, root, key, op.params)
		checkResponses(t, root, key, obj(b["responses"]))
	}
}

func checkParams(t *testing.T, root map[string]any, key string, params []any) {
	t.Helper()
	for _, p := range params {
		pm := resolve(root, p)
		name := str(pm["name"])
		if strings.TrimSpace(str(pm["description"])) == "" {
			t.Errorf("%s: parameter %s needs a description", key, name)
		}
		if pm["example"] == nil && pm["examples"] == nil && obj(pm["schema"])["example"] == nil {
			t.Errorf("%s: parameter %s needs an example", key, name)
		}
	}
}

func checkResponses(t *testing.T, root map[string]any, key string, responses map[string]any) {
	t.Helper()
	for _, code := range []string{"401", "429"} {
		if responses[code] == nil {
			t.Errorf("%s: missing %s response", key, code)
		}
	}
	success := false
	for code, rv := range responses {
		resp := resolve(root, rv)
		if strings.TrimSpace(str(resp["description"])) == "" {
			t.Errorf("%s %s: response needs a description", key, code)
		}
		if !strings.HasPrefix(code, "2") {
			continue
		}
		success = true
		for mt, mv := range obj(resp["content"]) {
			media := obj(mv)
			if str(obj(media["schema"])["$ref"]) == "" {
				t.Errorf("%s %s %s: schema must reference components/schemas", key, code, mt)
			}
			if media["example"] == nil && media["examples"] == nil {
				t.Errorf("%s %s %s: needs an example", key, code, mt)
			}
		}
	}
	if !success {
		t.Errorf("%s: needs a 2xx response", key)
	}
}

func TestSpecSchemasDocumented(t *testing.T) {
	root := loadSpec(t)
	for name, sv := range obj(obj(root["components"])["schemas"]) {
		s := obj(sv)
		if strings.TrimSpace(str(s["description"])) == "" {
			t.Errorf("schema %s needs a description", name)
		}
		if str(s["type"]) != "object" {
			continue
		}
		if ap, ok := s["additionalProperties"].(bool); !ok || ap {
			t.Errorf("schema %s must set additionalProperties: false", name)
		}
		props := obj(s["properties"])
		if len(props) == 0 {
			t.Errorf("schema %s has no properties", name)
		}
		for pn, pv := range props {
			p := obj(pv)
			if p["$ref"] == nil && strings.TrimSpace(str(p["description"])) == "" {
				t.Errorf("schema %s property %s needs a description", name, pn)
			}
		}
		for _, r := range arr(s["required"]) {
			if props[str(r)] == nil {
				t.Errorf("schema %s requires unknown property %s", name, str(r))
			}
		}
	}
}

func TestSpecExamplesMatchSchemas(t *testing.T) {
	root := loadSpec(t)
	doc := loadDoc(t)
	check := func(where string, media map[string]any) {
		ref := str(obj(media["schema"])["$ref"])
		if ref == "" {
			return
		}
		schema := schemaNamed(t, doc, strings.TrimPrefix(ref, "#/components/schemas/"))
		var examples []any
		if ex, ok := media["example"]; ok {
			examples = append(examples, ex)
		}
		for _, e := range obj(media["examples"]) {
			if v, ok := obj(e)["value"]; ok {
				examples = append(examples, v)
			}
		}
		for _, ex := range examples {
			if err := schema.VisitJSON(ex); err != nil {
				t.Errorf("%s: example does not match %s: %v", where, ref, err)
			}
		}
	}
	for _, op := range operations(root) {
		for code, rv := range obj(op.body["responses"]) {
			for mt, mv := range obj(resolve(root, rv)["content"]) {
				check(op.key()+" "+code+" "+mt, obj(mv))
			}
		}
	}
	for name, rv := range obj(obj(root["components"])["responses"]) {
		for mt, mv := range obj(obj(rv)["content"]) {
			check("components.responses."+name+" "+mt, obj(mv))
		}
	}
}

func TestHandlerTypesMatchSchemas(t *testing.T) {
	doc := loadDoc(t)
	at := time.Date(2026, 9, 10, 16, 56, 42, 62937000, time.FixedZone("MSK", 3*3600))
	cal := domain.Calendar{
		ID: domain.NewID(), Name: "Work", Color: "#3b6ea5", Timezone: "Europe/Moscow",
		Kind: domain.CalendarLocal, Position: 1, DefaultReminders: []int{10}, CreatedAt: at, UpdatedAt: at,
	}
	bare := cal
	bare.Timezone, bare.DefaultReminders = "", nil

	start := time.Date(2026, 9, 11, 7, 0, 0, 0, time.UTC)
	master := domain.Event{
		ID: domain.NewID(), CalendarID: cal.ID, UID: "standup", Title: "Standup", Start: start, End: start.Add(15 * time.Minute),
		TZ: "Europe/Moscow", RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", ExDate: []time.Time{start.AddDate(0, 0, 3)},
		Status: domain.StatusConfirmed, Transparency: domain.TransparencyOpaque, Visibility: domain.VisibilityDefault,
		Reminders: []int{10}, Version: 3, CreatedAt: at, UpdatedAt: at,
	}
	sid, day := master.ID, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	override := master
	override.ID, override.SeriesID, override.RecurrenceID, override.RRule, override.ExDate, override.Color = domain.NewID(), &sid, &start, "", nil, "#aa3355"
	allDay := domain.Event{
		ID: domain.NewID(), CalendarID: cal.ID, UID: "conf", Title: "Conference", AllDay: true, Start: day, End: day.AddDate(0, 0, 2),
		Status: domain.StatusTentative, Transparency: domain.TransparencyTransparent, Visibility: domain.VisibilityPrivate, Version: 1,
		CreatedAt: at, UpdatedAt: at,
	}

	list := domain.TodoList{ID: domain.NewID(), Name: "Inbox", Color: "#5b7c5a", IsDefault: true, CreatedAt: at, UpdatedAt: at}
	due, tm, pid := day, 18*60, domain.NewID()
	todo := domain.Todo{
		ID: domain.NewID(), ListID: list.ID, Title: "Assemble GPU node", Status: domain.TodoOpen, Priority: 2, Position: "V",
		DueDate: &due, DueTime: &tm, Duration: 90, TZ: "Europe/Moscow", ShowOnCalendar: true, Reminders: []int{30}, Version: 2,
		Checks:    []domain.Check{{ID: domain.NewID(), Text: "Risers", Done: true, Position: "V", DoneAt: &at}, {ID: domain.NewID(), Text: "Paste", Position: "l"}},
		CreatedAt: at, UpdatedAt: at,
	}
	done := domain.Todo{ID: domain.NewID(), ListID: list.ID, ParentID: &pid, Title: "Order fans", Status: domain.TodoCompleted, Position: "V", CompletedAt: &at, Version: 1, CreatedAt: at, UpdatedAt: at}

	samples := map[string]any{
		"User": toMe(domain.User{
			ID: domain.NewID(), Email: "sasha@akarpov.ru", DisplayName: "Sasha", Timezone: "Europe/Moscow", EmailVerifiedAt: &at,
			WeekStart: 1, TimeFormat: "24h", DefaultView: "week", SleepEnabled: true, CreatedAt: at,
		}, []domain.SleepWindow{{Weekday: time.Monday, Start: 23*60 + 30, End: 7*60 + 30}}),
		"Problem": Problem{
			Type:      "about:blank",
			Title:     "Unprocessable Entity",
			Status:    422,
			Detail:    "One or more fields are invalid.",
			Instance:  "/api/v1/me",
			RequestID: "tQ6DNZRbQcyrEPPA",
			Errors:    []domain.FieldError{{Field: "name", Message: "is required"}},
		},
		"Calendar":     toCalendarJSON(cal),
		"CalendarList": calendarList{Items: []calendarJSON{toCalendarJSON(cal), toCalendarJSON(bare)}},
		"Event":        eventResponse(master),
		"EventList": eventList{Items: []eventJSON{
			toEventJSON(domain.Occurrence{Event: master, Instance: &start}),
			eventResponse(override),
			eventResponse(allDay),
		}},
		"TodoList":       toTodoListJSON(list),
		"TodoListList":   todoListList{Items: []todoListJSON{toTodoListJSON(list)}},
		"Todo":           toTodoJSON(todo),
		"TodoPage":       todoPage{Items: []todoJSON{toTodoJSON(todo), toTodoJSON(done)}, NextCursor: optString(done.ID.String())},
		"TodoCompletion": completionJSON{Todo: toTodoJSON(done), NextID: optString(todo.ID.String())},
		"Check":          toCheckJSON(todo.Checks[0]),
		"CheckList":      toCheckList(todo.Checks),
		"Import": toImportJSON(domain.Import{
			ID: domain.NewID(), Source: domain.SourceICS, Filename: "work.ics", Status: domain.ImportPreviewed,
			CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour),
		}, &importer.Preview{
			Source: domain.SourceICS, Filename: "work.ics", Timezone: "Europe/Moscow",
			Targets:  []importer.Target{{Name: "Work", Kind: domain.BindCalendar, Target: importer.TargetNew, Events: 3, Index: 0}},
			Stats:    importer.Stats{New: 3},
			Samples:  []importer.Sample{{Where: "event 1", Title: "Standup", When: "2026-09-07 10:00", Action: "new"}},
			Problems: []transfer.Problem{{Where: "event 9", Message: "has no valid start"}},
		}),
		"Subscription": subscriptionJSON{
			ID: domain.NewID().String(), CalendarID: cal.ID.String(), URL: "https://example.com/calendar.ics",
			RefreshEvery: "1h0m0s", Status: domain.SubOK, NextRefreshAt: at, LastOKAt: &at,
		},
		"Duplicate": duplicateJSON{
			ID: domain.NewID().String(), Entity: domain.EntityEvent, Reason: domain.ReasonFingerprint, Score: 1,
			Status: domain.DupPending, CreatedAt: at,
			A: dedup.Snapshot{ID: domain.NewID().String(), Title: "Standup", When: "2026-09-07 10:00", Created: at.Format(time.RFC3339)},
			B: dedup.Snapshot{ID: domain.NewID().String(), Title: "Standup", When: "2026-09-07 10:00", Created: at.Format(time.RFC3339)},
		},
		"TrashItem": trashJSON{
			ID: domain.NewID().String(), Entity: domain.EntityEvent, Title: "Standup", When: "2026-09-07 10:00",
			Where: "Work", Children: 2, DeletedAt: at,
		},
		"Share": toShareJSON(domain.Share{
			ID: domain.NewID(), Name: "Week", View: "week", Period: at, Calendars: []domain.ID{cal.ID},
			Detail: domain.DetailTitles, TZMode: domain.ShareTZOwner, ShowSleep: true, Token: "2f8Qa1vX9c",
			AccessCount: 12, LastAccessedAt: &at, CreatedAt: at, UpdatedAt: at,
		}, "https://calendar.test"),
		"Change": changeJSON{
			Seq: 42, Entity: domain.EntityEvent, ID: domain.NewID().String(), Op: domain.OpUpdate,
			CalendarID: optString(cal.ID.String()), From: &at, To: &at, Origin: "google",
		},
		"SyncStatus": syncStatusJSON{
			Connected: true, Email: "sasha@gmail.com", Status: "ok",
			Bindings: []syncBindingJSON{{
				ID: domain.NewID().String(), Entity: domain.EntityEvent, RemoteID: "primary",
				RemoteName: "sasha@gmail.com", LocalID: cal.ID.String(), Direction: "both",
				Enabled: true, Watching: true, LastSyncedAt: &at,
			}},
		},
		"SearchResult": searchResponse{
			Query: "standup", Type: "",
			Items: []hitJSON{{
				Kind: "event", ID: domain.NewID().String(), Title: "Standup", Snippet: "Agenda",
				Container: "Work", Start: &at, End: &at,
			}},
		},
	}
	for name, v := range samples {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		if err := schemaNamed(t, doc, name).VisitJSON(decoded); err != nil {
			t.Errorf("%T does not match schema %s: %v", v, name, err)
		}
	}
}
