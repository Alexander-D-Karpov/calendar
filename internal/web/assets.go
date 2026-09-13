package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

type Assets struct {
	fsys   fs.FS
	dev    bool
	hashes map[string]string
}

func NewAssets(fsys fs.FS, dev bool) (*Assets, error) {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	a := &Assets{fsys: fsys, dev: dev, hashes: map[string]string{}}
	if dev {
		return a, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		h, err := hashFile(fsys, p)
		if err != nil {
			return err
		}
		a.hashes[p] = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Assets) URL(name string) string {
	u := "/static/" + name
	if h := a.hash(name); h != "" {
		u += "?v=" + h
	}
	return u
}

func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	if !fs.ValidPath(name) || name == "." || strings.HasPrefix(path.Base(name), ".") {
		http.NotFound(w, r)
		return
	}
	f, err := a.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	h := a.hash(name)
	w.Header().Set("ETag", `"`+h+`"`)
	if v := r.URL.Query().Get("v"); !a.dev && v != "" && v == h {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

func (a *Assets) hash(name string) string {
	if !a.dev {
		return a.hashes[name]
	}
	h, err := hashFile(a.fsys, name)
	if err != nil {
		return ""
	}
	return h
}

func hashFile(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12], nil
}

func (a *Assets) Has(name string) bool {
	if !a.dev {
		_, ok := a.hashes[name]
		return ok
	}
	st, err := fs.Stat(a.fsys, name)
	return err == nil && !st.IsDir()
}

func (a *Assets) ServeFile(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("path", name)
		w.Header().Set("Service-Worker-Allowed", "/")
		a.ServeHTTP(w, r)
	})
}
