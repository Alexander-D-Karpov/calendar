package domain

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrPrecondition = errors.New("precondition failed")
	ErrRateLimited  = errors.New("rate limited")
	ErrInvalid      = errors.New("invalid input")
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "invalid input: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalid
}

func (e *ValidationError) Add(field, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: message})
}

func (e *ValidationError) Addf(field, format string, args ...any) {
	e.Add(field, fmt.Sprintf(format, args...))
}

func (e *ValidationError) Err() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	return e
}
