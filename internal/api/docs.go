package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

	spec "github.com/Alexander-D-Karpov/calendar/api"
)

const maxYAMLDepth = 64

type document struct {
	body        []byte
	contentType string
	etag        string
}

type specDocs struct {
	yaml document
	json document
}

func newSpecDocs() (*specDocs, error) {
	j, err := yamlToJSON(spec.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("openapi spec: %w", err)
	}
	return &specDocs{
		yaml: newDocument(spec.OpenAPI, "application/yaml"),
		json: newDocument(j, "application/json"),
	}, nil
}

func newDocument(b []byte, contentType string) document {
	sum := sha256.Sum256(b)
	return document{body: b, contentType: contentType, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
}

func (d document) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", d.contentType)
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", d.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(d.body))
}

func yamlToJSON(src []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, errors.New("expected a single YAML document")
	}
	var buf bytes.Buffer
	if err := writeNode(&buf, root.Content[0], 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeNode(buf *bytes.Buffer, n *yaml.Node, depth int) error {
	if depth > maxYAMLDepth {
		return errors.New("yaml nesting is too deep")
	}
	switch n.Kind {
	case yaml.AliasNode:
		return writeNode(buf, n.Alias, depth+1)
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() == "!!merge" {
				return fmt.Errorf("line %d: unsupported mapping key", k.Line)
			}
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeJSON(buf, k.Value); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeNode(buf, n.Content[i+1], depth+1); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, c := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeNode(buf, c, depth+1); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		return writeScalar(buf, n)
	default:
		return fmt.Errorf("line %d: unsupported yaml node", n.Line)
	}
	return nil
}

func writeScalar(buf *bytes.Buffer, n *yaml.Node) error {
	switch n.ShortTag() {
	case "!!int", "!!float", "!!bool", "!!null":
		var v any
		if err := n.Decode(&v); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		if err := encodeJSON(buf, v); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		return nil
	}
	return encodeJSON(buf, n.Value)
}

func encodeJSON(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
