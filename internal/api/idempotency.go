package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const (
	IdempotencyHeader = "Idempotency-Key"
	replayedHeader    = "Idempotency-Replayed"
	maxKeyLength      = 255
)

type IdempotencyStore interface {
	ClaimIdempotency(ctx context.Context, user domain.ID, key, method, path string, hash []byte) (bool, error)
	Idempotency(ctx context.Context, user domain.ID, key string) (domain.IdempotentRecord, error)
	SaveIdempotency(ctx context.Context, user domain.ID, key string, status int, contentType string, body []byte) error
	DropIdempotency(ctx context.Context, user domain.ID, key string) error
}

var (
	errKeyReused  = fmt.Errorf("%w: Idempotency-Key was already used with a different request", domain.ErrInvalid)
	errInFlight   = fmt.Errorf("%w: the original request is still in flight", domain.ErrConflict)
	errKeyTooLong = fmt.Errorf("%w: Idempotency-Key must be 1 to 255 characters", domain.ErrInvalid)
)

// idempotency replays the stored reply when a POST repeats a key it has already
// seen, so a client that retries after a dropped connection does not create a
// second event.
func idempotency(store IdempotencyStore, fail auth.Fail) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyHeader)
			if key == "" || store == nil {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > maxKeyLength {
				fail(w, r, errKeyTooLong)
				return
			}
			owner := ownerID(r)
			if owner == domain.NilID {
				next.ServeHTTP(w, r)
				return
			}
			// The body has to be read to hash it, so hand the handler a fresh
			// reader over the same bytes.
			body, err := io.ReadAll(r.Body)
			if err != nil {
				fail(w, r, err)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))

			sum := sha256.New()
			sum.Write([]byte(r.Method))
			sum.Write([]byte(r.URL.Path))
			sum.Write(body)
			hash := sum.Sum(nil)

			ctx := r.Context()
			fresh, err := store.ClaimIdempotency(ctx, owner, key, r.Method, r.URL.Path, hash)
			if err != nil {
				fail(w, r, err)
				return
			}
			if !fresh {
				rec, err := store.Idempotency(ctx, owner, key)
				if err != nil {
					fail(w, r, err)
					return
				}
				switch {
				case !bytes.Equal(rec.RequestHash, hash):
					fail(w, r, errKeyReused)
				case rec.Status == 0:
					fail(w, r, errInFlight)
				default:
					replay(w, rec)
				}
				return
			}

			rec, info := httpx.Record(w)
			buf := &bytes.Buffer{}
			next.ServeHTTP(&captureWriter{ResponseWriter: rec, buf: buf}, r)

			status := info.StatusCode()
			if status < 200 || status > 299 {
				// Leave the key free so the caller can fix the request and retry.
				_ = store.DropIdempotency(context.WithoutCancel(ctx), owner, key)
				return
			}
			_ = store.SaveIdempotency(context.WithoutCancel(ctx), owner, key, status, w.Header().Get("Content-Type"), buf.Bytes())
		})
	}
}

func replay(w http.ResponseWriter, rec domain.IdempotentRecord) {
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	w.Header().Set(replayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// captureWriter keeps a copy of the body so a replay can return the same bytes.
type captureWriter struct {
	http.ResponseWriter
	buf *bytes.Buffer
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.buf.Write(p)
	return c.ResponseWriter.Write(p)
}
