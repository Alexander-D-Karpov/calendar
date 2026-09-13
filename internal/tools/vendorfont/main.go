package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	releaseAPI = "https://api.github.com/repos/rsms/inter/releases/"
	maxZip     = 64 << 20
	maxFile    = 8 << 20
)

// The OG renderer draws headings in SemiBold and everything else in Regular.
var wanted = map[string]string{
	"InterDisplay-Regular.otf":  "Inter-Regular.ttf",
	"InterDisplay-SemiBold.otf": "Inter-SemiBold.ttf",
	"Inter-Regular.otf":         "Inter-Regular.ttf",
	"Inter-SemiBold.otf":        "Inter-SemiBold.ttf",
	"Inter_18pt-Regular.ttf":    "Inter-Regular.ttf",
	"Inter_18pt-SemiBold.ttf":   "Inter-SemiBold.ttf",
	"Inter-Regular.ttf":         "Inter-Regular.ttf",
	"Inter-SemiBold.ttf":        "Inter-SemiBold.ttf",
}

var (
	required  = []string{"Inter-Regular.ttf", "Inter-SemiBold.ttf"}
	versionRe = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,64}$`)
)

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func main() {
	version := flag.String("version", "latest", "rsms/inter release tag, or latest")
	out := flag.String("out", "web/static/fonts", "output directory")
	flag.Parse()
	if err := run(*version, *out); err != nil {
		fmt.Fprintln(os.Stderr, "vendor-font:", err)
		os.Exit(1)
	}
}

func run(version, out string) error {
	if !versionRe.MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	endpoint := releaseAPI + "tags/" + version
	if version == "latest" {
		endpoint = releaseAPI + "latest"
	}
	body, err := fetch(client, endpoint, 1<<20)
	if err != nil {
		return err
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return fmt.Errorf("decode release: %w", err)
	}
	if !versionRe.MatchString(rel.TagName) {
		return fmt.Errorf("unexpected release tag %q", rel.TagName)
	}
	asset := ""
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, ".zip") && strings.Contains(strings.ToLower(a.Name), "inter") {
			asset = a.URL
			break
		}
	}
	if asset == "" {
		return errors.New("release has no Inter zip asset")
	}
	blob, err := fetch(client, asset, maxZip)
	if err != nil {
		return err
	}
	files, err := extract(blob)
	if err != nil {
		return err
	}
	for _, name := range required {
		if _, ok := files[name]; !ok {
			return fmt.Errorf("the release zip has no source for %s", name)
		}
	}
	if err := write(out, rel.TagName, files); err != nil {
		return err
	}
	fmt.Printf("vendored Inter %s into %s\n", rel.TagName, out)
	return nil
}

func fetch(c *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
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

// extract keeps the first match for each target, so a zip carrying both the
// variable and the static build does not depend on walk order.
func extract(blob []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		target, ok := wanted[path.Base(f.Name)]
		if !ok || out[target] != nil {
			continue
		}
		if f.UncompressedSize64 > maxFile {
			return nil, fmt.Errorf("%s is larger than %d bytes", f.Name, maxFile)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[target] = b
	}
	if lic := findLicense(zr); lic != nil {
		out["LICENSE"] = lic
	}
	return out, nil
}

func findLicense(zr *zip.Reader) []byte {
	for _, f := range zr.File {
		name := strings.ToUpper(path.Base(f.Name))
		if !strings.HasPrefix(name, "LICENSE") && !strings.HasPrefix(name, "OFL") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxFile))
		_ = rc.Close()
		if err == nil {
			return b
		}
	}
	return nil
}

func write(out, version string, files map[string][]byte) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(out, name), body, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "VERSION"), []byte(version+"\n"), 0o644)
}
