package output

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table"} {
		var out bytes.Buffer
		err := Write(&out, Envelope([]any{map[string]any{"id": 123, "name": "hello"}}, map[string]any{}), format, false)
		if err != nil {
			t.Fatal(err)
		}
		switch format {
		case "json":
			if !json.Valid(out.Bytes()) {
				t.Fatal(out.String())
			}
		case "yaml":
			var v any
			if err := yaml.Unmarshal(out.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
		case "table":
			if !strings.Contains(out.String(), "hello") {
				t.Fatal(out.String())
			}
		}
	}
}
func TestTableSanitizesControls(t *testing.T) {
	var out bytes.Buffer
	if err := Write(&out, Envelope([]any{map[string]any{"id": 1, "name": "name\n\x1b[31m\u202espoof"}}, map[string]any{}), "table", false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\u202e") {
		t.Fatal(out.String())
	}
}

func TestYAMLPreservesLargeIntegers(t *testing.T) {
	var out bytes.Buffer
	if err := Write(&out, Envelope(json.RawMessage(`{"id":9007199254740993}`), map[string]any{}), "yaml", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "9007199254740993") {
		t.Fatal(out.String())
	}
}
