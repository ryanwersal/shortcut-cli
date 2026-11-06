// Package contract derives commands and validation from the pinned Shortcut OpenAPI contract.
package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed openapi.json
var Spec []byte

type Parameter struct {
	In          string         `json:"in"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Required    bool           `json:"required"`
	Schema      map[string]any `json:"schema"`
	Explode     *bool          `json:"explode,omitempty"`
}
type Body struct {
	Required bool `json:"required"`
	Content  map[string]struct {
		Schema map[string]any `json:"schema"`
	} `json:"content"`
}
type Operation struct {
	ID          string         `json:"operationId"`
	Summary     string         `json:"summary"`
	Description string         `json:"description"`
	Parameters  []Parameter    `json:"parameters,omitempty"`
	RequestBody *Body          `json:"requestBody,omitempty"`
	Responses   map[string]any `json:"responses"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Safety      string         `json:"safety"`
	Resource    string         `json:"resource"`
	Command     string         `json:"command"`
}
type Contract struct {
	Operations []Operation
	document   map[string]any
}

var camel = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func Load() (*Contract, error) {
	var doc map[string]any
	if err := json.Unmarshal(Spec, &doc); err != nil {
		return nil, err
	}
	c := &Contract{document: doc}
	paths := doc["paths"].(map[string]any)
	for path, methods := range paths {
		for method, raw := range methods.(map[string]any) {
			if method != "get" && method != "post" && method != "put" && method != "delete" && method != "patch" {
				continue
			}
			rawMap := raw.(map[string]any)
			if body, ok := rawMap["requestBody"].(map[string]any); ok {
				if ref, ok := body["$ref"].(string); ok {
					rawMap["requestBody"] = c.lookup(ref)
				}
			}
			b, _ := json.Marshal(rawMap)
			var op Operation
			if err := json.Unmarshal(b, &op); err != nil {
				return nil, err
			}
			op.Method = strings.ToUpper(method)
			op.Path = path
			op.Safety = "write"
			if method == "get" || op.ID == "queryStories" {
				op.Safety = "read"
			}
			op.Resource = strings.Split(strings.TrimPrefix(path, "/api/v3/"), "/")[0]
			op.Command = strings.ToLower(camel.ReplaceAllString(op.ID, "${1}-${2}"))
			singular := map[string]string{"categories": "category", "stories": "story", "repositories": "repository", "queries": "query", "documents": "doc"}[op.Resource]
			if singular == "" {
				singular = strings.TrimSuffix(op.Resource, "s")
			}
			// Retain explicit action names while avoiding redundant resource names.
			op.Command = strings.Replace(op.Command, "-"+op.Resource, "", 1)
			op.Command = regexp.MustCompile("-"+regexp.QuoteMeta(singular)+"($|-)").ReplaceAllString(op.Command, "$1")
			if op.Command == "" {
				op.Command = "get"
			}
			if op.Resource == "search" {
				op.Command = strings.TrimPrefix(op.Command, "search-")
				if op.Command == "search" {
					op.Command = "all"
				}
			}
			if name, ok := map[string]string{"linkDocumentToEpic": "link-epic", "unlinkDocumentFromEpic": "unlink-epic", "listStoryComment": "list-comments", "storyHistory": "history", "createEpicCommentComment": "reply-comment"}[op.ID]; ok {
				op.Command = name
			}
			c.Operations = append(c.Operations, op)
		}
	}
	sort.Slice(c.Operations, func(i, j int) bool { return c.Operations[i].ID < c.Operations[j].ID })
	seen := map[string]bool{}
	for _, op := range c.Operations {
		k := op.Safety + " " + op.Resource + " " + op.Command
		if seen[k] {
			return nil, fmt.Errorf("duplicate command %s", k)
		}
		seen[k] = true
	}
	return c, nil
}
func (c *Contract) lookup(ref string) any {
	var cur any = c.document
	for _, p := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = cur.(map[string]any)[p]
	}
	return cur
}
func (c *Contract) Find(id string) (Operation, bool) {
	for _, o := range c.Operations {
		if o.ID == id {
			return o, true
		}
	}
	return Operation{}, false
}

// InputSchema includes only referenced definitions so agent discovery stays bounded.
func (c *Contract) InputSchema(op Operation) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range op.Parameters {
		props[p.Name] = p.Schema
		if p.Required {
			required = append(required, p.Name)
		}
	}
	root := map[string]any{"type": "object", "properties": props, "additionalProperties": false, "required": required}
	if op.RequestBody != nil {
		for media, v := range op.RequestBody.Content {
			if media == "multipart/form-data" {
				props["file"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 4}
				props["story_id"] = map[string]any{"type": "integer", "minimum": 1}
				required = append(required, "file")
			}
			if media == "application/json" {
				props["body"] = v.Schema
				if op.RequestBody.Required {
					required = append(required, "body")
				}
			}
		}
		root["required"] = required
	}
	defs := map[string]any{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["$ref"].(string); ok {
				if _, ok := defs[r]; !ok {
					defs[r] = nil
					defs[r] = c.lookup(r)
					walk(defs[r])
				}
			}
			for _, v := range x {
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(root)
	schemas := map[string]any{}
	for ref, s := range defs {
		schemas[strings.TrimPrefix(ref, "#/components/schemas/")] = s
	}
	root["components"] = map[string]any{"schemas": schemas}
	normalized := normalize(root).(map[string]any)
	// The endpoint prose requires exactly one of these fields; upstream's schema
	// currently marks only name as required. Expose the same rule in discovery.
	normalizedSchemas := normalized["components"].(map[string]any)["schemas"].(map[string]any)
	if story, ok := normalizedSchemas["CreateStoryParams"].(map[string]any); ok {
		story["oneOf"] = []any{map[string]any{"required": []string{"workflow_state_id"}}, map[string]any{"required": []string{"project_id"}}}
	}
	return normalized
}

// OpenAPI 3.0 nullable is converted to JSON Schema without changing the pinned source.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		n := map[string]any{}
		for k, v := range x {
			if k != "nullable" {
				n[k] = normalize(v)
			}
		}
		if x["nullable"] == true {
			return map[string]any{"anyOf": []any{n, map[string]any{"type": "null"}}}
		}
		return n
	case []any:
		n := make([]any, len(x))
		for i, v := range x {
			n[i] = normalize(v)
		}
		return n
	default:
		return v
	}
}
func (c *Contract) Validate(op Operation, input map[string]any) error {
	data, err := json.Marshal(c.InputSchema(op))
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7
	if err = compiler.AddResource("input.json", bytes.NewReader(data)); err != nil {
		return err
	}
	schema, err := compiler.Compile("input.json")
	if err != nil {
		return err
	}
	return schema.Validate(input)
}
