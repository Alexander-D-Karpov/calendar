package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ByteSize int64

const (
	B  ByteSize = 1
	KB          = 1024 * B
	MB          = 1024 * KB
	GB          = 1024 * MB
	TB          = 1024 * GB
)

var sizeUnits = []struct {
	suffix string
	size   ByteSize
}{
	{"TB", TB}, {"GB", GB}, {"MB", MB}, {"KB", KB},
	{"T", TB}, {"G", GB}, {"M", MB}, {"K", KB},
	{"B", B},
}

func ParseByteSize(s string) (ByteSize, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	unit := B
	for _, u := range sizeUnits {
		if strings.HasSuffix(t, u.suffix) {
			unit = u.size
			t = strings.TrimSpace(strings.TrimSuffix(t, u.suffix))
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n > math.MaxInt64/int64(unit) {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return ByteSize(n) * unit, nil
}

func (b ByteSize) Int64() int64 {
	return int64(b)
}

func (b ByteSize) String() string {
	for _, u := range sizeUnits[:4] {
		if b >= u.size && b%u.size == 0 {
			return strconv.FormatInt(int64(b/u.size), 10) + u.suffix
		}
	}
	return strconv.FormatInt(int64(b), 10) + "B"
}

type Rate struct {
	Count int
	Per   time.Duration
}

func ParseRate(s string) (Rate, error) {
	count, per, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		return Rate{}, fmt.Errorf("invalid rate %q, expected count/duration like 10/15m", s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n <= 0 {
		return Rate{}, fmt.Errorf("invalid rate count in %q", s)
	}
	per = strings.TrimSpace(per)
	if per != "" && (per[0] < '0' || per[0] > '9') {
		per = "1" + per
	}
	d, err := time.ParseDuration(per)
	if err != nil || d <= 0 {
		return Rate{}, fmt.Errorf("invalid rate period in %q", s)
	}
	return Rate{Count: n, Per: d}, nil
}

func (r Rate) String() string {
	return strconv.Itoa(r.Count) + "/" + formatDuration(r.Per)
}

func (r Rate) Interval() time.Duration {
	if r.Count <= 0 {
		return r.Per
	}
	return r.Per / time.Duration(r.Count)
}

func formatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

type SecretKeys map[uint32][]byte

func ParseSecretKeys(s string) (SecretKeys, error) {
	keys := SecretKeys{}
	for i, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idText, encoded, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("entry %d: expected id:base64", i+1)
		}
		id, err := strconv.ParseUint(strings.TrimSpace(idText), 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("entry %d: key id must be a positive integer", i+1)
		}
		if _, dup := keys[uint32(id)]; dup {
			return nil, fmt.Errorf("duplicate key id %d", id)
		}
		raw, err := decodeBase64(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("key %d: invalid base64", id)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("key %d: must decode to 32 bytes, got %d", id, len(raw))
		}
		keys[uint32(id)] = raw
	}
	if len(keys) == 0 {
		return nil, errors.New("no keys defined")
	}
	return keys, nil
}

func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func (k SecretKeys) IDs() []uint32 {
	ids := make([]uint32, 0, len(k))
	for id := range k {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (k SecretKeys) Highest() uint32 {
	ids := k.IDs()
	if len(ids) == 0 {
		return 0
	}
	return ids[len(ids)-1]
}

func (k SecretKeys) Masked() string {
	parts := make([]string, 0, len(k))
	for _, id := range k.IDs() {
		parts = append(parts, strconv.FormatUint(uint64(id), 10)+":****")
	}
	return strings.Join(parts, ",")
}

func ParseList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range ParseList(s) {
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q", item)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q", item)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
