package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

type Form struct {
	Values url.Values
	Errors map[string]string
	Error  string
}

func NewForm(v url.Values) *Form {
	if v == nil {
		v = url.Values{}
	}
	return &Form{Values: v, Errors: map[string]string{}}
}

func (f *Form) Get(key string) string {
	return strings.TrimSpace(f.Values.Get(key))
}

// Merge overlays values onto a copy of the form. Existing keys win, so an
// explicit field always beats something inferred from the text.
func (f *Form) Merge(v url.Values) *Form {
	out := make(url.Values, len(f.Values)+len(v))
	for k, vals := range v {
		out[k] = vals
	}
	for k, vals := range f.Values {
		if len(vals) == 1 && vals[0] == "" {
			continue
		}
		out[k] = vals
	}
	return &Form{Values: out, Errors: f.Errors, Error: f.Error}
}

func (f *Form) Has(key, value string) bool {
	return slices.Contains(f.Values[key], value)
}

func (f *Form) Err(key string) string {
	return f.Errors[key]
}

func (f *Form) Invalid() bool {
	return f.Error != "" || len(f.Errors) > 0
}

func (f *Form) SetError(err error) bool {
	var ve *domain.ValidationError
	var rl *ratelimit.Error
	switch {
	case errors.As(err, &ve):
		for _, fe := range ve.Fields {
			if _, ok := f.Errors[fe.Field]; !ok {
				f.Errors[fe.Field] = sentence(fe.Message)
			}
		}
	case errors.As(err, &rl):
		f.Error = "Too many attempts. Try again in " + humanWait(rl.RetryAfter) + "."
	case errors.Is(err, auth.ErrInvalidCredentials):
		f.Error = "Incorrect email or password."
	case errors.Is(err, auth.ErrDisabled):
		f.Error = "This account is disabled."
	case errors.Is(err, auth.ErrRegistrationClosed):
		f.Error = "Registration is closed."
	case errors.Is(err, auth.ErrUnverified):
		f.Error = "Confirm your email address first. Check your inbox."
	case errors.Is(err, auth.ErrBadToken):
		f.Error = "This link expired or was already used. Ask for a new one."
	case errors.Is(err, auth.ErrNoPassword):
		f.Error = "This account has no password."
	case errors.Is(err, auth.ErrEmailUnverified):
		f.Error = "The Google account email is not verified."
	case errors.Is(err, auth.ErrDomainNotAllowed):
		f.Error = "This email domain cannot register."
	case errors.Is(err, auth.ErrIdentityTaken):
		f.Error = "This Google account is linked to another user."
	case errors.Is(err, auth.ErrAlreadyLinked):
		f.Error = "A Google account is already linked. Unlink it first."
	case errors.Is(err, auth.ErrLastLogin):
		f.Error = "Set a password before unlinking Google, it is your only way to sign in."
	case errors.Is(err, auth.ErrWrongIdentity):
		f.Error = "Sign in with the Google account linked to this user."
	default:
		return false
	}
	return true
}

func (s *Server) ParseForm(w http.ResponseWriter, r *http.Request) (*Form, bool) {
	if err := r.ParseForm(); err != nil {
		s.Fail(w, r, fmt.Errorf("%w: %w", domain.ErrInvalid, err))
		return nil, false
	}
	v := url.Values{}
	for k, vals := range r.PostForm {
		switch k {
		case "password", "password_confirm", "current_password", auth.CSRFField:
		default:
			v[k] = vals
		}
	}
	return NewForm(v), true
}

func ErrorText(err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) && len(ve.Fields) > 0 {
		fe := ve.Fields[0]
		return sentence(strings.ReplaceAll(fe.Field, "_", " ") + " " + fe.Message)
	}
	var pgErr *pgconn.PgError
	if errors.Is(err, domain.ErrConflict) && !errors.As(err, &pgErr) {
		if _, msg, ok := strings.Cut(err.Error(), ": "); ok {
			return sentence(msg)
		}
	}
	f := NewForm(nil)
	if f.SetError(err) {
		return f.Error
	}
	return ""
}

func (f *Form) Any(keys ...string) bool {
	for _, k := range keys {
		if f.Errors[k] != "" {
			return true
		}
	}
	return false
}
