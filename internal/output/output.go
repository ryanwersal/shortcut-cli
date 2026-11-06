package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"

	"gopkg.in/yaml.v3"
)

func Envelope(data, meta any) map[string]any {
	return map[string]any{"schema_version": "1", "data": data, "meta": meta}
}
func Write(w io.Writer, value any, format string, compact bool) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if format == "json" {
		if !compact {
			b, err = json.MarshalIndent(value, "", "  ")
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	}
	var plain any
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if err = decoder.Decode(&plain); err != nil {
		return err
	}
	if format == "yaml" { // Preserve numeric precision by decoding JSON directly into YAML nodes.
		var node yaml.Node
		if err = yaml.Unmarshal(b, &node); err != nil {
			return err
		}
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(&node)
	}
	root, ok := plain.(map[string]any)
	if !ok {
		return fmt.Errorf("expected response envelope")
	}
	data := root["data"]
	rows, ok := data.([]any)
	if !ok {
		rows = []any{data}
	}
	if len(rows) == 0 {
		_, err = fmt.Fprintln(w, "No results.")
		return err
	}
	columns := []string{}
	for _, key := range []string{"id", "name", "title", "story_type", "workflow_state_id", "state", "operationId", "command", "safety", "summary"} {
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok && m[key] != nil {
				columns = append(columns, key)
				break
			}
		}
	}
	if len(columns) == 0 {
		return Write(w, data, "json", compact)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(columns, "\t")))
	for _, row := range rows {
		m, _ := row.(map[string]any)
		cells := []string{}
		for _, key := range columns {
			v := ""
			if m[key] != nil {
				v = fmt.Sprint(m[key])
			}
			v = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
					return ' '
				}
				return r
			}, v)
			r := []rune(v)
			if len(r) > 80 {
				v = string(r[:79]) + "…"
			}
			cells = append(cells, v)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}
