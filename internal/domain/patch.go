package domain

import (
	"bytes"
	"encoding/json"
)

type Opt[T any] struct {
	Set  bool
	Null bool
	V    T
}

func Some[T any](v T) Opt[T] {
	return Opt[T]{Set: true, V: v}
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		var zero T
		o.Null, o.V = true, zero
		return nil
	}
	o.Null = false
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(&o.V)
}

func (o Opt[T]) Value(v *ValidationError, field string) (T, bool) {
	if !o.Set {
		return o.V, false
	}
	if o.Null {
		v.Add(field, "must not be null")
		return o.V, false
	}
	return o.V, true
}
