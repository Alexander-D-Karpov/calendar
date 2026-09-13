package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type fakeIdem struct {
	mu   sync.Mutex
	rows map[string]*domain.IdempotentRecord
}

func newFakeIdem() *fakeIdem {
	return &fakeIdem{rows: map[string]*domain.IdempotentRecord{}}
}

func (f *fakeIdem) ClaimIdempotency(_ context.Context, user domain.ID, key, _, _ string, hash []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := user.String() + "|" + key
	if _, ok := f.rows[k]; ok {
		return false, nil
	}
	f.rows[k] = &domain.IdempotentRecord{RequestHash: hash}
	return true, nil
}

func (f *fakeIdem) Idempotency(_ context.Context, user domain.ID, key string) (domain.IdempotentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.rows[user.String()+"|"+key]
	if !ok {
		return domain.IdempotentRecord{}, domain.ErrNotFound
	}
	return *rec, nil
}

func (f *fakeIdem) SaveIdempotency(_ context.Context, user domain.ID, key string, status int, ctype string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.rows[user.String()+"|"+key]
	if !ok {
		return domain.ErrNotFound
	}
	rec.Status, rec.ContentType, rec.Body = status, ctype, body
	return nil
}

func (f *fakeIdem) DropIdempotency(_ context.Context, user domain.ID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, user.String()+"|"+key)
	return nil
}

func idemHandler(t *testing.T, store IdempotencyStore, h http.HandlerFunc) (http.Handler, *int) {
	t.Helper()
	calls := 0
	owner := domain.NewID()
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		h(w, r)
	})
	mw := idempotency(store, func(w http.ResponseWriter, _ *http.Request, err error) {
		w.WriteHeader(statusOf(err))
		_, _ = io.WriteString(w, err.Error())
	})
	withPrincipal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithPrincipal(r.Context(), &auth.Principal{UserID: owner})
		mw(counted).ServeHTTP(w, r.WithContext(ctx))
	})
	return withPrincipal, &calls
}

func statusOf(err error) int {
	switch {
	case strings.Contains(err.Error(), "still in flight"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

func post(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	r.Header.Set("Content-Type", mediaJSON)
	if key != "" {
		r.Header.Set(IdempotencyHeader, key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func created(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", mediaJSON)
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"id":"made-once"}`)
}

func TestIdempotencyReplaysTheFirstReply(t *testing.T) {
	h, calls := idemHandler(t, newFakeIdem(), created)
	first := post(t, h, "abc", `{"title":"Standup"}`)
	if first.Code != http.StatusCreated || first.Header().Get(replayedHeader) != "" {
		t.Fatalf("first = %d %q", first.Code, first.Header().Get(replayedHeader))
	}
	second := post(t, h, "abc", `{"title":"Standup"}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d", second.Code)
	}
	if second.Header().Get(replayedHeader) != "true" {
		t.Error("a replay must say so")
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Errorf("replay body = %q, want %q", second.Body, first.Body)
	}
	if *calls != 1 {
		t.Fatalf("the handler ran %d times, want 1", *calls)
	}
}

func TestIdempotencyRejectsADifferentBody(t *testing.T) {
	h, calls := idemHandler(t, newFakeIdem(), created)
	post(t, h, "abc", `{"title":"Standup"}`)
	reused := post(t, h, "abc", `{"title":"Something else"}`)
	if reused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", reused.Code)
	}
	if *calls != 1 {
		t.Fatalf("the handler ran %d times, want 1", *calls)
	}
}

func TestIdempotencyFreesTheKeyAfterAFailure(t *testing.T) {
	store := newFakeIdem()
	fails := true
	h, calls := idemHandler(t, store, func(w http.ResponseWriter, r *http.Request) {
		if fails {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		created(w, r)
	})
	if got := post(t, h, "abc", `{}`).Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("first = %d", got)
	}
	fails = false
	if got := post(t, h, "abc", `{}`).Code; got != http.StatusCreated {
		t.Fatalf("retry after a failure = %d, want 201", got)
	}
	if *calls != 2 {
		t.Fatalf("the handler ran %d times, want 2", *calls)
	}
}

// A 500 must free the key too. Holding it would leave every retry hitting the
// in-flight branch, wedging the caller on that key forever.
func TestIdempotencyFreesTheKeyAfterAServerError(t *testing.T) {
	store := newFakeIdem()
	fails := true
	h, calls := idemHandler(t, store, func(w http.ResponseWriter, r *http.Request) {
		if fails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		created(w, r)
	})
	if got := post(t, h, "abc", `{}`).Code; got != http.StatusInternalServerError {
		t.Fatalf("first = %d", got)
	}
	fails = false
	second := post(t, h, "abc", `{}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("retry after a 500 = %d, want 201 (not a wedged 409)", second.Code)
	}
	if *calls != 2 {
		t.Fatalf("the handler ran %d times, want 2", *calls)
	}
}

func TestIdempotencyIgnoredWithoutAKey(t *testing.T) {
	h, calls := idemHandler(t, newFakeIdem(), created)
	post(t, h, "", `{}`)
	post(t, h, "", `{}`)
	if *calls != 2 {
		t.Fatalf("without a key every request runs: %d", *calls)
	}
}

func TestIdempotencyRejectsALongKey(t *testing.T) {
	h, calls := idemHandler(t, newFakeIdem(), created)
	if got := post(t, h, strings.Repeat("k", maxKeyLength+1), `{}`).Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", got)
	}
	if *calls != 0 {
		t.Fatal("an invalid key must not reach the handler")
	}
}
