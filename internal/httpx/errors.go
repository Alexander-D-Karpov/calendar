package httpx

import (
	"errors"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string {
	return e.Msg
}

func StatusFor(err error) int {
	var se *StatusError
	switch {
	case err == nil:
		return http.StatusOK
	case errors.As(err, &se):
		return se.Code
	case IsBodyTooLarge(err):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, domain.ErrPrecondition):
		return http.StatusPreconditionFailed
	case errors.Is(err, domain.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}
