package domain

import (
	"errors"
	"testing"
)

func TestValidationError(t *testing.T) {
	var v ValidationError
	if v.Err() != nil {
		t.Fatal("empty validation must be nil")
	}
	v.Add("title", "is too long")
	v.Addf("color", "invalid %q", "red")
	err := v.Err()
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("must wrap ErrInvalid")
	}
	if err.Error() != `invalid input: title: is too long; color: invalid "red"` {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestIDs(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || a.Version() != 7 {
		t.Fatalf("ids = %s %s", a, b)
	}
	if a.String() >= b.String() {
		t.Fatal("v7 ids must sort by creation time")
	}
	if got, err := ParseID(a.String()); err != nil || got != a {
		t.Fatalf("ParseID = %v %v", got, err)
	}
	for _, bad := range []string{"", "x", "00000000-0000-0000-0000-000000000000", "{" + a.String() + "}", "urn:uuid:" + a.String()} {
		if _, err := ParseID(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseID(%q) err = %v", bad, err)
		}
	}
}
