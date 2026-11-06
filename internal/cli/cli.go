package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ryanwersal/shortcut-cli/internal/contract"
	"github.com/ryanwersal/shortcut-cli/internal/output"
	"github.com/ryanwersal/shortcut-cli/internal/shortcut"
	"github.com/spf13/cobra"
)

const maxInputBytes = 10 * 1024 * 1024

var Version = "dev"

type options struct {
	format            string
	compact, readOnly bool
	timeout           time.Duration
	retries           int
	maxBytes          int64
}

func Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) int {
	root, err := New(in, out)
	if err == nil {
		root.SetArgs(args)
		err = root.ExecuteContext(ctx)
	}
	if err == nil {
		return 0
	}
	var e *shortcut.Error
	if !errors.As(err, &e) {
		e = &shortcut.Error{Code: "invalid_input", Message: err.Error(), Exit: 2}
	}
	// Errors remain JSON for table output, matching wand. Never echo the API token.
	message := e.Message
	if token := os.Getenv("SHORTCUT_API_TOKEN"); token != "" {
		message = strings.ReplaceAll(message, token, "[REDACTED]")
	}
	format := "json"
	for i, arg := range args {
		if arg == "--output=yaml" || (arg == "--output" && i+1 < len(args) && args[i+1] == "yaml") {
			format = "yaml"
		}
	}
	_ = output.Write(stderr, map[string]any{"schema_version": "1", "error": map[string]any{"code": e.Code, "message": message}}, format, false)
	return e.Exit
}
func New(in io.Reader, out io.Writer) (*cobra.Command, error) {
	c, err := contract.Load()
	if err != nil {
		return nil, err
	}
	o := &options{}
	root := &cobra.Command{Use: "sc", Short: "Read and explicitly write Shortcut workspace data", Version: Version, SilenceErrors: true, SilenceUsage: true}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(out)
	root.PersistentFlags().StringVar(&o.format, "output", "json", "Output format: json, yaml, table")
	root.PersistentFlags().BoolVar(&o.compact, "compact", false, "Emit compact JSON")
	root.PersistentFlags().BoolVar(&o.readOnly, "read-only", false, "Reject every write operation, including previews")
	root.PersistentFlags().DurationVar(&o.timeout, "timeout", 30*time.Second, "Timeout per HTTP request")
	root.PersistentFlags().IntVar(&o.retries, "retries", 2, "Retries for read responses with HTTP 429 or 5xx (0..5); writes never retry")
	root.PersistentFlags().Int64Var(&o.maxBytes, "max-response-bytes", 10*1024*1024, "Maximum response size (1..52428800 bytes)")
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if o.format != "json" && o.format != "yaml" && o.format != "table" {
			return shortcut.Fail("invalid_input", 2, "output must be json, yaml, or table")
		}
		return nil
	}
	read := &cobra.Command{Use: "read", Short: "READ operations: never modify workspace data"}
	write := &cobra.Command{Use: "write", Short: "WRITE operations: modify workspace data; require --execute"}
	root.AddCommand(read, write)
	groups := map[string]*cobra.Command{}
	for _, op := range c.Operations {
		parent := read
		if op.Safety == "write" {
			parent = write
		}
		key := op.Safety + "/" + op.Resource
		group := groups[key]
		if group == nil {
			group = &cobra.Command{Use: op.Resource, Short: strings.ToUpper(op.Safety) + " " + op.Resource + " operations"}
			groups[key] = group
			parent.AddCommand(group)
		}
		group.AddCommand(operationCommand(c, op, o))
	}
	// Search aggregation is separate from the single-page API operation.
	var search shortcut.SearchOptions
	searchCmd := &cobra.Command{Use: "search", Short: "READ: search stories with bounded pagination", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := o.client()
		if err != nil {
			return err
		}
		rows, meta, err := client.Search(cmd.Context(), search)
		if err != nil {
			return err
		}
		return o.render(cmd, rows, meta)
	}}
	searchCmd.Flags().StringVar(&search.Query, "query", "", "Shortcut search query (required)")
	searchCmd.Flags().StringVar(&search.Cursor, "cursor", "", "Resume from meta.next_cursor")
	searchCmd.Flags().IntVar(&search.Limit, "limit", 100, "Maximum results (1..1000)")
	searchCmd.Flags().IntVar(&search.PageSize, "page-size", 25, "Results per request (1..250)")
	searchCmd.Flags().IntVar(&search.MaxPages, "max-pages", 10, "Maximum pages (1..1000)")
	groups["read/stories"].AddCommand(searchCmd)
	auth := &cobra.Command{Use: "auth", Short: "Authentication checks"}
	auth.AddCommand(&cobra.Command{Use: "check", Short: "READ: verify token using the current member endpoint", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := o.client()
		if err != nil {
			return err
		}
		data, err := client.Get(cmd.Context(), "member")
		if err != nil {
			return err
		}
		return o.render(cmd, data, map[string]any{"safety": "read"})
	}})
	root.AddCommand(auth)
	agent := &cobra.Command{Use: "agent", Short: "Local, credential-free command discovery"}
	var operation string
	schema := &cobra.Command{Use: "schema", Short: "Describe commands, safety, inputs, and output contracts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if operation != "" {
			op, ok := c.Find(operation)
			if !ok {
				return shortcut.Fail("invalid_input", 2, "unknown operation %q", operation)
			}
			return o.render(cmd, map[string]any{"operation": op, "input_schema": c.InputSchema(op)}, map[string]any{})
		}
		commands := []any{}
		for _, op := range c.Operations {
			commands = append(commands, map[string]any{"operationId": op.ID, "command": "sc " + op.Safety + " " + op.Resource + " " + op.Command, "safety": op.Safety, "method": op.Method, "summary": op.Summary, "requires_execute": op.Safety == "write"})
		}
		return o.render(cmd, map[string]any{"name": "sc", "version": Version, "api_version": "v3", "commands": commands, "local_commands": []string{"agent schema", "agent openapi", "completions <shell>"}, "additional_read_commands": []string{"auth check", "read stories search"}, "write_policy": "write namespace AND --execute; --dry-run is local; writes never retry; --read-only rejects all writes", "configuration": []string{"SHORTCUT_API_TOKEN"}, "output": map[string]any{"default": "json", "formats": []string{"json", "yaml", "table"}, "success_required": []string{"schema_version", "data", "meta"}, "error_required": []string{"schema_version", "error.code", "error.message"}}, "exit_codes": map[string]string{"0": "success", "1": "API/response/I/O failure", "2": "configuration or input", "3": "authentication or authorization", "4": "not found", "5": "rate limit or transport"}}, map[string]any{})
	}}
	schema.Flags().StringVar(&operation, "operation", "", "Show request schema by OpenAPI operation ID")
	agent.AddCommand(schema, &cobra.Command{Use: "openapi", Short: "Print the pinned upstream OpenAPI document", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := cmd.OutOrStdout().Write(contract.Spec)
		return err
	}})
	root.AddCommand(agent)
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(&cobra.Command{Use: "completions <bash|zsh|fish|powershell>", Short: "Generate shell completions locally", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		default:
			return shortcut.Fail("invalid_input", 2, "unsupported shell")
		}
	}})
	return root, nil
}
func (o *options) client() (*shortcut.Client, error) {
	return shortcut.New(os.Getenv("SHORTCUT_API_TOKEN"), o.timeout, o.maxBytes, o.retries)
}
func (o *options) render(cmd *cobra.Command, data, meta any) error {
	if err := output.Write(cmd.OutOrStdout(), output.Envelope(data, meta), o.format, o.compact); err != nil {
		return shortcut.Fail("io_error", 1, "could not write output: %v", err)
	}
	return nil
}
func flagName(name string) string { return strings.ReplaceAll(name, "_", "-") }
func operationCommand(c *contract.Contract, op contract.Operation, o *options) *cobra.Command {
	var body, bodyFile string
	var execute, dryRun, schema bool
	var files []string
	var storyID int64
	cmd := &cobra.Command{Use: op.Command, Short: strings.ToUpper(op.Safety) + ": " + op.Summary, Long: strings.ToUpper(op.Safety) + ": " + op.Description + "\n\nOperation: " + op.ID + "\n" + op.Method + " " + op.Path, Args: cobra.NoArgs}
	values := map[string]*string{}
	arrays := map[string]*[]string{}
	for _, p := range op.Parameters {
		help := p.Description
		if p.Required {
			help += " (required)"
		}
		name := flagName(p.Name)
		if p.Schema["type"] == "array" {
			arrays[p.Name] = cmd.Flags().StringArray(name, nil, help+"; repeat for multiple values")
		} else {
			values[p.Name] = cmd.Flags().String(name, "", help)
		}
	}
	cmd.Flags().BoolVar(&schema, "schema", false, "Show this operation's input schema without credentials or requests")
	if op.Safety == "write" {
		cmd.Flags().BoolVar(&execute, "execute", false, "EXPLICITLY AUTHORIZE this write to the Shortcut workspace")
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and preview this write locally; do not send it")
	}
	multipartBody := false
	if op.RequestBody != nil {
		_, multipartBody = op.RequestBody.Content["multipart/form-data"]
		if multipartBody {
			cmd.Flags().StringArrayVar(&files, "file", nil, "File to upload; repeat up to four times")
			cmd.Flags().Int64Var(&storyID, "story-id", 0, "Optional story to associate with uploads")
		} else {
			cmd.Flags().StringVar(&body, "body", "", "Request body as JSON; use --schema to inspect fields")
			cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read JSON body from a file or - for stdin")
		}
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if schema {
			return o.render(cmd, c.InputSchema(op), map[string]any{"operationId": op.ID, "safety": op.Safety})
		}
		if op.Safety == "write" {
			if o.readOnly {
				return shortcut.Fail("invalid_input", 2, "--read-only forbids write operations")
			}
			if execute && dryRun {
				return shortcut.Fail("invalid_input", 2, "--execute and --dry-run are mutually exclusive")
			}
			if !execute && !dryRun {
				return shortcut.Fail("invalid_input", 2, "WRITE operation: pass --dry-run to preview or --execute to authorize this change")
			}
		}
		input := map[string]any{}
		path := op.Path
		q := url.Values{}
		for _, p := range op.Parameters {
			name := flagName(p.Name)
			if !cmd.Flags().Changed(name) {
				continue
			}
			var v any
			var err error
			if a, ok := arrays[p.Name]; ok {
				items := []any{}
				itemSchema, _ := p.Schema["items"].(map[string]any)
				for _, s := range *a {
					item, e := parseValue(s, itemSchema)
					if e != nil {
						return e
					}
					items = append(items, item)
				}
				v = items
			} else {
				v, err = parseValue(*values[p.Name], p.Schema)
				if err != nil {
					return err
				}
			}
			input[p.Name] = v
			if p.In == "path" {
				s := fmt.Sprint(v)
				if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\?#%") {
					return shortcut.Fail("invalid_input", 2, "invalid path parameter %s", name)
				}
				path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(s))
			} else if p.In == "query" {
				if arr, ok := v.([]any); ok {
					parts := []string{}
					for _, item := range arr {
						parts = append(parts, fmt.Sprint(item))
					}
					if p.Explode != nil && !*p.Explode {
						q.Set(p.Name, strings.Join(parts, ","))
					} else {
						for _, s := range parts {
							q.Add(p.Name, s)
						}
					}
				} else {
					q.Set(p.Name, fmt.Sprint(v))
				}
			}
		}
		var payload []byte
		contentType := "application/json"
		if cmd.Flags().Changed("body") && cmd.Flags().Changed("body-file") {
			return shortcut.Fail("invalid_input", 2, "--body and --body-file are mutually exclusive")
		}
		if bodyFile != "" {
			var r io.Reader = cmd.InOrStdin()
			if bodyFile != "-" {
				f, err := os.Open(bodyFile)
				if err != nil {
					return shortcut.Fail("io_error", 1, "cannot open body file: %v", err)
				}
				defer f.Close()
				r = f
			}
			b, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
			if err != nil {
				return shortcut.Fail("io_error", 1, "cannot read body")
			}
			body = string(b)
		}
		if len(body) > maxInputBytes {
			return shortcut.Fail("invalid_input", 2, "JSON body exceeds 10 MiB")
		}
		if body != "" {
			dec := json.NewDecoder(strings.NewReader(body))
			dec.UseNumber()
			var v any
			if dec.Decode(&v) != nil {
				return shortcut.Fail("invalid_input", 2, "body must be valid JSON")
			}
			if dec.Decode(new(any)) != io.EOF {
				return shortcut.Fail("invalid_input", 2, "body must contain one JSON value")
			}
			input["body"] = v
			payload = []byte(body)
		}
		if multipartBody {
			fileInputs := make([]any, len(files))
			for i, path := range files {
				fileInputs[i] = path
			}
			input["file"] = fileInputs
			if cmd.Flags().Changed("story-id") {
				input["story_id"] = storyID
			}
		}
		if err := c.Validate(op, input); err != nil {
			return shortcut.Fail("invalid_input", 2, "request does not match %s schema: %v", op.ID, err)
		}
		if multipartBody {
			var err error
			payload, contentType, err = uploadBody(files, storyID)
			if err != nil {
				return err
			}
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		meta := map[string]any{"operationId": op.ID, "safety": op.Safety, "method": op.Method}
		if dryRun {
			meta["dry_run"] = true
			preview := map[string]any{"method": op.Method, "path": path, "input": input}
			if multipartBody {
				preview["files"] = files
				preview["story_id"] = storyID
			}
			return o.render(cmd, preview, meta)
		}
		client, err := o.client()
		if err != nil {
			return err
		}
		data, err := client.Do(cmd.Context(), op.Method, path, payload, contentType, op.Safety == "read")
		if err != nil {
			return err
		}
		return o.render(cmd, data, meta)
	}
	return cmd
}
func parseValue(s string, schema map[string]any) (any, error) {
	switch schema["type"] {
	case "integer":
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return nil, shortcut.Fail("invalid_input", 2, "expected integer")
		}
		return json.Number(s), nil
	case "number":
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, shortcut.Fail("invalid_input", 2, "expected number")
		}
		return v, nil
	case "boolean":
		if s != "true" && s != "false" {
			return nil, shortcut.Fail("invalid_input", 2, "expected true or false")
		}
		return s == "true", nil
	default:
		return s, nil
	}
}
func uploadBody(files []string, storyID int64) ([]byte, string, error) {
	if len(files) < 1 || len(files) > 4 || storyID < 0 {
		return nil, "", shortcut.Fail("invalid_input", 2, "provide 1..4 --file values and a nonnegative story-id")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for i, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, "", shortcut.Fail("io_error", 1, "cannot open upload: %v", err)
		}
		part, err := w.CreateFormFile(fmt.Sprintf("file%d", i), filepath.Base(path))
		if err != nil {
			f.Close()
			return nil, "", err
		}
		remaining := int64(maxInputBytes - buf.Len())
		n, err := io.Copy(part, io.LimitReader(f, remaining+1))
		f.Close()
		if err != nil {
			return nil, "", shortcut.Fail("io_error", 1, "cannot read upload")
		}
		if n > remaining {
			return nil, "", shortcut.Fail("invalid_input", 2, "uploads exceed combined 10 MiB limit")
		}
	}
	if storyID > 0 {
		if err := w.WriteField("story_id", strconv.FormatInt(storyID, 10)); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}
