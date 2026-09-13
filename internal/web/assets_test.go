package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"css/a.css": {Data: []byte("body{}")},
		"js/app.js": {Data: []byte("x")},
	}
	a, err := NewAssets(fsys, false)
	if err != nil {
		t.Fatal(err)
	}
	u := a.URL("css/a.css")
	if !strings.HasPrefix(u, "/static/css/a.css?v=") || len(u) != len("/static/css/a.css?v=")+12 {
		t.Fatalf("URL = %q", u)
	}
	if a.URL("missing.css") != "/static/missing.css" {
		t.Fatalf("missing URL = %q", a.URL("missing.css"))
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/{path...}", a)
	get := func(target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get(u, nil)
	if rec.Code != 200 || rec.Body.String() != "body{}" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("versioned = %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("versioned cache = %q", rec.Header().Get("Cache-Control"))
	}
	if cc := get("/static/css/a.css", nil).Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("unversioned cache = %q", cc)
	}
	if rec := get("/static/css/a.css?v=stale", nil); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("stale version must not be immutable")
	}
	if rec := get("/static/css/a.css", map[string]string{"If-None-Match": rec.Header().Get("ETag")}); rec.Code != http.StatusNotModified {
		t.Fatalf("conditional = %d", rec.Code)
	}
	for _, p := range []string{"/static/css", "/static/nope.css", "/static/"} {
		if rec := get(p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
}

func TestAssetsHas(t *testing.T) {
	fsys := fstest.MapFS{"vendor/x.js": {Data: []byte("x")}}
	for _, dev := range []bool{false, true} {
		a, err := NewAssets(fsys, dev)
		if err != nil {
			t.Fatal(err)
		}
		if !a.Has("vendor/x.js") || a.Has("vendor/y.js") || a.Has("vendor") {
			t.Errorf("dev=%v: Has mismatch", dev)
		}
	}
}
