package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	registry   = "https://registry.npmjs.org/swagger-ui-dist/"
	tarballURL = "https://registry.npmjs.org/swagger-ui-dist/-/"
	maxTarball = 32 << 20
	maxFile    = 16 << 20
)

var (
	wanted    = []string{"swagger-ui-bundle.js", "swagger-ui.css", "LICENSE", "NOTICE"}
	required  = []string{"swagger-ui-bundle.js", "swagger-ui.css"}
	sourceMap = regexp.MustCompile(`(?m)^[ \t]*(//# sourceMappingURL=\S*|/\*# sourceMappingURL=\S* \*/)[ \t]*\r?\n?`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,64}$`)
)

type manifest struct {
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func main() {
	version := flag.String("version", "latest", "swagger-ui-dist version or dist-tag")
	out := flag.String("out", "web/static/vendor/swagger-ui", "output directory")
	flag.Parse()
	if err := run(*version, *out); err != nil {
		fmt.Fprintln(os.Stderr, "vendor-swagger:", err)
		os.Exit(1)
	}
}

func run(version, out string) error {
	if !versionRe.MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	body, err := fetch(client, registry+url.PathEscape(version), 1<<20)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if !versionRe.MatchString(m.Version) || !strings.HasPrefix(m.Dist.Tarball, tarballURL) {
		return errors.New("unexpected registry manifest")
	}
	tarball, err := fetch(client, m.Dist.Tarball, maxTarball)
	if err != nil {
		return err
	}
	if err := verifyIntegrity(tarball, m.Dist.Integrity); err != nil {
		return err
	}
	files, err := extract(tarball)
	if err != nil {
		return err
	}
	for _, name := range required {
		if _, ok := files[name]; !ok {
			return fmt.Errorf("tarball is missing %s", name)
		}
	}
	if err := write(out, m.Version, files); err != nil {
		return err
	}
	fmt.Printf("vendored swagger-ui-dist %s into %s\n", m.Version, out)
	return nil
}

func fetch(c *http.Client, u string, limit int64) ([]byte, error) {
	resp, err := c.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", u, limit)
	}
	return b, nil
}

func verifyIntegrity(b []byte, integrity string) error {
	algo, want, ok := strings.Cut(integrity, "-")
	if !ok || algo != "sha512" {
		return fmt.Errorf("unsupported integrity %q", integrity)
	}
	sum := sha512.Sum512(b)
	if base64.StdEncoding.EncodeToString(sum[:]) != want {
		return errors.New("tarball integrity check failed")
	}
	return nil
}

func extract(tarball []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := strings.CutPrefix(path.Clean(h.Name), "package/")
		if !ok || !slices.Contains(wanted, rel) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxFile+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxFile {
			return nil, fmt.Errorf("%s is too large", rel)
		}
		if strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".css") {
			data = sourceMap.ReplaceAll(data, nil)
		}
		files[rel] = data
	}
	return files, nil
}

func write(out, version string, files map[string][]byte) error {
	tmp := out + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	var sums strings.Builder
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(tmp, n), files[n], 0o644); err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(files[n]), n)
	}
	if err := os.WriteFile(filepath.Join(tmp, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}
