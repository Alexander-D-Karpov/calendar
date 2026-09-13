package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	spec "github.com/Alexander-D-Karpov/calendar/api"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

func TestYAMLToJSON(t *testing.T) {
	src := []byte("b: 1\na:\n  - true\n  - null\n  - 1.5\n  - \"200\"\n  - 2026-09-10\n  - &x {k: v}\n  - *x\n\"200\": ok\nq: 'Bearer realm=\"x\"'\n")
	got, err := yamlToJSON(src)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":1,"a":[true,null,1.5,"200","2026-09-10",{"k":"v"},{"k":"v"}],"200":"ok","q":"Bearer realm=\"x\""}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, bad := range []string{"", "a: .inf", "base: &b {x: 1}\nc:\n  <<: *b\n", "[unclosed"} {
		if _, err := yamlToJSON([]byte(bad)); err == nil {
			t.Errorf("yamlToJSON(%q) expected error", bad)
		}
	}
}

func TestRoutesServeSpec(t *testing.T) {
	mux := http.NewServeMux()
	err := Routes(mux, Deps{
		Limiter: ratelimit.New(config.Rate{Count: 10, Per: time.Minute}, nil),
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/api/openapi.json", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("json = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(rec.Body.String(), `{"openapi":"3.0.3","info":`) {
		t.Fatalf("json must keep document order: %.60s", rec.Body.String())
	}
	if again := get("/api/openapi.json", map[string]string{"If-None-Match": rec.Header().Get("ETag")}); again.Code != http.StatusNotModified {
		t.Fatalf("conditional = %d", again.Code)
	}

	rec = get("/api/openapi.yaml", nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), spec.OpenAPI) {
		t.Fatalf("yaml = %d", rec.Code)
	}

	rec = get("/api/v1/nope", nil)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
