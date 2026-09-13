package og

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := Key("share-1", "v1")
	if _, ok := c.Get(key); ok {
		t.Fatal("an empty cache must miss")
	}
	body := []byte("not really a png")
	c.Put(key, body)
	got, ok := c.Get(key)
	if !ok || !bytes.Equal(got, body) {
		t.Fatalf("get = %q %v", got, ok)
	}
	// The shard directory is only created when something lands in it.
	if _, err := os.Stat(filepath.Join(c.dir, key[:2])); err != nil {
		t.Errorf("shard dir: %v", err)
	}
}

func TestKeyVaries(t *testing.T) {
	a, b := Key("share-1", "v1"), Key("share-1", "v2")
	if a == b {
		t.Fatal("a new version must be a new key")
	}
	if Key("share-2", "v1") == a {
		t.Fatal("a different share must be a different key")
	}
	if len(a) != 16 {
		t.Fatalf("key length = %d", len(a))
	}
}

func TestEvictDropsTheOldestFirst(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 300, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{'x'}, 100)
	keys := []string{Key("a", "v"), Key("b", "v"), Key("c", "v"), Key("d", "v")}
	for i, k := range keys {
		c.Put(k, body)
		// Space the mtimes so "oldest" is unambiguous.
		when := time.Now().Add(time.Duration(i-len(keys)) * time.Hour)
		if err := os.Chtimes(c.path(k), when, when); err != nil {
			t.Fatal(err)
		}
	}
	total, err := c.Evict()
	if err != nil {
		t.Fatal(err)
	}
	if total > 300 {
		t.Errorf("cache is %d bytes, want at most 300", total)
	}
	if _, ok := c.Get(keys[0]); ok {
		t.Error("the oldest entry must go first")
	}
	if _, ok := c.Get(keys[3]); !ok {
		t.Error("the newest entry must survive")
	}
}

func TestEvictLeavesASmallCacheAlone(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := Key("a", "v")
	c.Put(key, bytes.Repeat([]byte{'x'}, 100))
	total, err := c.Evict()
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
	if _, ok := c.Get(key); !ok {
		t.Error("nothing should be evicted under the limit")
	}
}

func TestNilCacheIsInert(t *testing.T) {
	var c *Cache
	c.Put("k", []byte("x"))
	if _, ok := c.Get("k"); ok {
		t.Fatal("a nil cache must always miss")
	}
	if n, err := c.Evict(); n != 0 || err != nil {
		t.Fatalf("evict = %d %v", n, err)
	}
}
