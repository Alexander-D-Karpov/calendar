package og

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Cache struct {
	dir   string
	max   int64
	gauge prometheus.Gauge
}

func NewCache(dir string, max int64, gauge prometheus.Gauge) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("og: cache dir is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, max: max, gauge: gauge}, nil
}

// Key is short on purpose: it names a file, and the full hash buys nothing once
// the version is already part of the input.
func Key(shareID, version string) string {
	sum := sha256.Sum256([]byte(shareID + "|" + version))
	return hex.EncodeToString(sum[:])[:16]
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key+".png")
}

// Get bumps the file's mtime so eviction can treat it as least-recently-used.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	b, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(c.path(key), now, now)
	return b, true
}

// Put writes through a temporary file so a reader never sees a half-written
// image, and shards lazily so an unused cache stays a single directory.
func (c *Cache) Put(key string, body []byte) {
	if c == nil {
		return
	}
	path := c.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
	}
}

type entry struct {
	path string
	size int64
	mod  time.Time
}

// Evict deletes the oldest files until the cache is under its limit and reports
// the size it settled at.
func (c *Cache) Evict() (int64, error) {
	if c == nil {
		return 0, nil
	}
	var (
		files []entry
		total int64
	)
	err := filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".png" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, entry{path: path, size: info.Size(), mod: info.ModTime()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	if c.max > 0 && total > c.max {
		sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
		for _, f := range files {
			if total <= c.max {
				break
			}
			if os.Remove(f.path) == nil {
				total -= f.size
			}
		}
	}
	if c.gauge != nil {
		c.gauge.Set(float64(total))
	}
	return total, nil
}
