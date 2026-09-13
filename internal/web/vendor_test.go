package web

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"testing"

	webfs "github.com/Alexander-D-Karpov/calendar/web"
)

const swaggerDir = "static/vendor/swagger-ui"

func TestVendoredSwaggerChecksums(t *testing.T) {
	sums, err := fs.ReadFile(webfs.FS, swaggerDir+"/SHA256SUMS")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("swagger ui is not vendored, run: make vendor-swagger")
	}
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		want, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("malformed line %q", sc.Text())
		}
		data, err := fs.ReadFile(webfs.FS, swaggerDir+"/"+name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			t.Errorf("%s does not match SHA256SUMS, re-run make vendor-swagger", name)
		}
		listed[name] = true
	}
	for _, want := range []string{"swagger-ui-bundle.js", "swagger-ui.css"} {
		if !listed[want] {
			t.Errorf("%s is not vendored", want)
		}
	}
	entries, err := fs.ReadDir(webfs.FS, swaggerDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "SHA256SUMS" && n != "VERSION" && !listed[n] {
			t.Errorf("unexpected file %s in %s", n, swaggerDir)
		}
	}
}
