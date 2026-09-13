package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

func normText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func EventFingerprint(e Event) []byte {
	h := sha256.New()
	h.Write([]byte(normText(e.Title)))
	h.Write([]byte{0})
	h.Write([]byte(normText(e.Location)))
	var b [17]byte
	binary.BigEndian.PutUint64(b[0:], uint64(e.Start.UTC().Unix()))
	binary.BigEndian.PutUint64(b[8:], uint64(e.End.UTC().Unix()))
	if e.AllDay {
		b[16] = 1
	}
	h.Write(b[:])
	return h.Sum(nil)[:16]
}

func TodoFingerprint(t Todo) []byte {
	h := sha256.New()
	h.Write([]byte(normText(t.Title)))
	h.Write([]byte{0})
	if t.DueDate != nil {
		h.Write([]byte(t.DueDate.Format(DateLayout)))
	}
	h.Write([]byte{0})
	if t.DueTime != nil {
		h.Write([]byte(FormatClock(*t.DueTime)))
	}
	return h.Sum(nil)[:16]
}
