package web

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const maxFormMemory = 1 << 20

type Upload struct {
	Filename string
	Body     []byte
}

func (s *Server) Upload(w http.ResponseWriter, r *http.Request, field string, maxSize int64) (Upload, *Form, bool) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		s.Fail(w, r, &httpx.StatusError{Code: http.StatusUnsupportedMediaType, Msg: "send the file as multipart/form-data"})
		return Upload{}, nil, false
	}
	if err := r.ParseMultipartForm(maxFormMemory); err != nil {
		s.Fail(w, r, uploadError(err))
		return Upload{}, nil, false
	}
	values := url.Values{}
	if r.MultipartForm != nil {
		for k, v := range r.MultipartForm.Value {
			values[k] = v
		}
	}
	f := NewForm(values)
	file, head, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		f.Errors[field] = "Choose a file."
		return Upload{}, f, false
	}
	if err != nil {
		s.Fail(w, r, uploadError(err))
		return Upload{}, nil, false
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		s.Fail(w, r, uploadError(err))
		return Upload{}, nil, false
	}
	switch {
	case int64(len(body)) > maxSize:
		f.Errors[field] = fmt.Sprintf("The file is larger than %s.", ByteSize(maxSize))
		return Upload{}, f, false
	case len(body) == 0:
		f.Errors[field] = "The file is empty."
		return Upload{}, f, false
	}
	return Upload{Filename: SafeFilename(head), Body: body}, f, true
}

func uploadError(err error) error {
	if httpx.IsBodyTooLarge(err) {
		return err
	}
	return fmt.Errorf("%w: the upload could not be read", domain.ErrInvalid)
}

func SafeFilename(head *multipart.FileHeader) string {
	if head == nil {
		return "upload"
	}
	name := filepath.Base(strings.ReplaceAll(head.Filename, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`"/\`, r) {
			return -1
		}
		return r
	}, name)
	if name = strings.TrimSpace(name); name == "" || name == "." || name == ".." {
		return "upload"
	}
	return Clip(name, 255)
}

func ByteSize(n int64) string {
	units := []struct {
		size int64
		name string
	}{{1 << 30, "GB"}, {1 << 20, "MB"}, {1 << 10, "KB"}}
	for _, u := range units {
		if n >= u.size {
			return fmt.Sprintf("%d%s", n/u.size, u.name)
		}
	}
	return fmt.Sprintf("%dB", n)
}

func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (s *Server) SendFile(w http.ResponseWriter, name, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}
