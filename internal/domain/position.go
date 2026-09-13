package domain

import "strings"

const positionDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func KeyBetween(a, b string) string {
	if b != "" && a >= b {
		b = ""
	}
	switch {
	case b == "":
		return keyAfter(a)
	case a == "":
		return keyBefore(b)
	}
	return midpoint(a, b)
}

func keyAfter(a string) string {
	for i := 0; i < len(a); i++ {
		if d := digit(a[i]); d < len(positionDigits)-1 {
			return a[:i] + string(positionDigits[d+1])
		}
	}
	return a + "V"
}

func keyBefore(b string) string {
	for i := 0; i < len(b); i++ {
		switch d := digit(b[i]); {
		case d > 1:
			return b[:i] + string(positionDigits[d-1])
		case d == 1:
			return b[:i] + "0V"
		}
	}
	return midpoint("", b)
}

func midpoint(a, b string) string {
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + midpoint(rest, b[n:])
		}
	}
	da, db := 0, len(positionDigits)
	if a != "" {
		da = digit(a[0])
	}
	if b != "" {
		db = digit(b[0])
	}
	if db-da > 1 {
		return string(positionDigits[(da+db+1)/2])
	}
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if a != "" {
		rest = a[1:]
	}
	return string(positionDigits[da]) + midpoint(rest, "")
}

func digit(c byte) int {
	return max(0, strings.IndexByte(positionDigits, c))
}
