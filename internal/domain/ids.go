package domain

import (
	"fmt"

	"github.com/google/uuid"
)

type ID = uuid.UUID

var NilID = uuid.Nil

func NewID() ID {
	return uuid.Must(uuid.NewV7())
}

func ParseID(s string) (ID, error) {
	if len(s) != 36 {
		return uuid.Nil, fmt.Errorf("%w: invalid id", ErrInvalid)
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: invalid id", ErrInvalid)
	}
	return id, nil
}
