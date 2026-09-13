package config

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// .env.example is the only documentation of what can be configured, so it drifts
// silently the moment a key is added to load.go and nowhere else.
func TestEnvExampleCoversEveryKey(t *testing.T) {
	path := filepath.Join("..", "..", ".env.example")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	documented := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("%s: line is not KEY=value: %q", path, line)
			continue
		}
		documented[strings.TrimSpace(key)] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	// The file ships an empty SECRET_KEYS placeholder, which fails validation;
	// the environment wins over the file, so this only supplies what is needed
	// to reach the entry list.
	t.Setenv("SECRET_KEYS", "1:"+testKey(3))
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, e := range cfg.Entries() {
		known[e.Key] = true
	}

	// Read by the integration test harness rather than by Config, but it still
	// belongs in the example so a contributor knows to set it.
	known["TEST_DATABASE_URL"] = true

	var missing, extra []string
	for key := range known {
		if !documented[key] {
			missing = append(missing, key)
		}
	}
	for key := range documented {
		if !known[key] {
			extra = append(extra, key)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	if len(missing) > 0 {
		t.Errorf("%s does not document: %s", path, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("%s documents keys the config does not read: %s", path, strings.Join(extra, ", "))
	}
}
