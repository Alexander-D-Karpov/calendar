package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const (
	mediaJSON       = "application/json"
	mediaMergePatch = "application/merge-patch+json"
)

type apiFunc func(w http.ResponseWriter, r *http.Request) error

func (f apiFunc) serve(fail auth.Fail) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			fail(w, r, err)
		}
	})
}

func ownerID(r *http.Request) domain.ID {
	return auth.PrincipalFrom(r.Context()).UserID
}

func pathID(r *http.Request) (domain.ID, error) {
	return pathParam(r, "id")
}

func pathParam(r *http.Request, name string) (domain.ID, error) {
	id, err := domain.ParseID(r.PathValue(name))
	if err != nil {
		return domain.NilID, domain.ErrNotFound
	}
	return id, nil
}

func decodeJSON(r *http.Request, v any, mediaTypes ...string) error {
	if len(mediaTypes) == 0 {
		mediaTypes = []string{mediaJSON}
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !slices.Contains(mediaTypes, mt) {
		return &httpx.StatusError{Code: http.StatusUnsupportedMediaType, Msg: "content type must be " + strings.Join(mediaTypes, " or ")}
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body must contain a single JSON value", domain.ErrInvalid)
	}
	return nil
}

func decodeError(err error) error {
	if httpx.IsBodyTooLarge(err) {
		return err
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		var v domain.ValidationError
		v.Addf(te.Field, "must be a JSON %s", te.Value)
		return v.Err()
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body is empty", domain.ErrInvalid)
	}
	return fmt.Errorf("%w: %s", domain.ErrInvalid, strings.TrimPrefix(err.Error(), "json: "))
}

func writeResource(w http.ResponseWriter, r *http.Request, status int, etag string, v any) {
	w.Header().Set("ETag", etag)
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && domain.MatchETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, status, v)
}

func methodNotAllowed(methods []string, fail auth.Fail) http.Handler {
	list := slices.Clone(methods)
	if slices.Contains(list, http.MethodGet) {
		list = append(list, http.MethodHead)
	}
	slices.Sort(list)
	allow := strings.Join(slices.Compact(list), ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		fail(w, r, &httpx.StatusError{Code: http.StatusMethodNotAllowed, Msg: "method not allowed, use " + allow})
	})
}
