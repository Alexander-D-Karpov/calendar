package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"
)

type Renderer struct {
	fsys  fs.FS
	dev   bool
	funcs template.FuncMap
	pages map[string]*template.Template
}

func NewRenderer(fsys fs.FS, dev bool, funcs template.FuncMap) (*Renderer, error) {
	r := &Renderer{fsys: fsys, dev: dev, funcs: funcs}
	pages, err := r.load()
	if err != nil {
		return nil, err
	}
	r.pages = pages
	return r, nil
}

func (r *Renderer) Execute(w io.Writer, name string, data any) error {
	return r.ExecuteBlock(w, name, "pages/"+name+".html", data)
}

func (r *Renderer) ExecuteBlock(w io.Writer, name, block string, data any) error {
	pages := r.pages
	if r.dev {
		var err error
		if pages, err = r.load(); err != nil {
			return err
		}
	}
	t, ok := pages[name]
	if !ok {
		return fmt.Errorf("web: unknown page %q", name)
	}
	return t.ExecuteTemplate(w, block, data)
}

func (r *Renderer) load() (map[string]*template.Template, error) {
	base := template.New("").Funcs(r.funcs)
	for _, dir := range []string{"layouts", "partials"} {
		if err := r.walk(dir, func(p string, src []byte) error {
			_, err := base.New(p).Parse(string(src))
			return err
		}); err != nil {
			return nil, err
		}
	}
	pages := map[string]*template.Template{}
	err := r.walk("pages", func(p string, src []byte) error {
		t, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := t.New(p).Parse(string(src)); err != nil {
			return err
		}
		pages[strings.TrimSuffix(strings.TrimPrefix(p, "pages/"), ".html")] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pages, nil
}

func (r *Renderer) walk(dir string, fn func(string, []byte) error) error {
	return fs.WalkDir(r.fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		src, err := fs.ReadFile(r.fsys, p)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(src)) == 0 {
			return nil
		}
		if err := fn(p, src); err != nil {
			return fmt.Errorf("template %s: %w", p, err)
		}
		return nil
	})
}
