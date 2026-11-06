package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/ryanwersal/shortcut-cli/internal/contract"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, err bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &out, &err)
	return code, out.String(), err.String()
}
func TestDiscoveryWithoutCredentials(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "")
	for _, args := range [][]string{{"--help"}, {"agent", "schema"}, {"agent", "schema", "--operation", "createStory"}, {"write", "stories", "create", "--schema"}, {"completions", "bash"}, {"completions", "zsh"}, {"completions", "fish"}, {"completions", "powershell"}} {
		code, out, err := run(t, args...)
		if code != 0 || out == "" || err != "" {
			t.Fatalf("%v: %d %s", args, code, err)
		}
	}
}
func TestExplicitWriteGuards(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "do-not-send")
	for _, args := range [][]string{
		{"write", "stories", "delete", "--story-public-id", "123"},
		{"write", "stories", "delete", "--story-public-id", "123", "--execute=false"},
		{"--read-only", "write", "stories", "delete", "--story-public-id", "123", "--execute"},
		{"write", "stories", "delete", "--story-public-id", "123", "--execute", "--dry-run"},
	} {
		code, out, err := run(t, args...)
		if code != 2 || out != "" || !json.Valid([]byte(err)) {
			t.Fatalf("guard failed %v: %d %s %s", args, code, out, err)
		}
	}
}
func TestEveryWriteRequiresExecute(t *testing.T) {
	root, err := New(strings.NewReader(""), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range root.Commands() {
		if group.Name() != "write" {
			continue
		}
		for _, resource := range group.Commands() {
			for _, op := range resource.Commands() {
				code, _, stderr := run(t, "write", resource.Name(), op.Name())
				if code != 2 || !strings.Contains(stderr, "--execute") {
					t.Errorf("unguarded %s: %s", op.CommandPath(), stderr)
				}
			}
		}
	}
}
func TestDryRunValidation(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "")
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"name":"hello","workflow_state_id":123}`, 0},
		{`{"name":"hello","workflow_state_id":"wrong"}`, 2},
		{`{"name":"hello","workflow_state_id":123,"typo":true}`, 2},
		{`{} {}`, 2},
	} {
		code, out, err := run(t, "write", "stories", "create", "--dry-run", "--body", tc.body)
		if code != tc.code {
			t.Fatalf("%d %s %s", code, out, err)
		}
		if code == 0 && !strings.Contains(out, `"dry_run": true`) {
			t.Fatal(out)
		}
	}
}
func TestReadValidationAndAuth(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "")
	for _, args := range [][]string{{"read", "stories", "get"}, {"read", "stories", "get", "--story-public-id", "abc"}, {"read", "stories", "get", "--story-public-id", "123"}, {"read", "stories", "get", "--story-public-id", "123", "--execute"}, {"--output", "bad", "agent", "schema"}} {
		code, out, err := run(t, args...)
		if code != 2 || out != "" || !json.Valid([]byte(err)) {
			t.Fatalf("%v %d %s %s", args, code, out, err)
		}
	}
}
func TestBodyFromStdin(t *testing.T) {
	var out, err bytes.Buffer
	code := Run(context.Background(), []string{"write", "stories", "update", "--story-public-id", "1", "--dry-run", "--body-file", "-"}, strings.NewReader(`{"estimate":null}`), &out, &err)
	if code != 0 {
		t.Fatal(err.String())
	}
	if !strings.Contains(out.String(), `"estimate": null`) {
		t.Fatal(out.String())
	}
}
func TestRedactsTokenFromErrors(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "sensitive-token")
	_, _, err := run(t, "sensitive-token")
	if strings.Contains(err, "sensitive-token") {
		t.Fatal(err)
	}
}

// intercept keeps command execution tests offline while observing the complete
// request produced by the real command and HTTP client.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func intercept(t *testing.T, f roundTripFunc) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = old })
}
func TestExecuteWrite(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "local-test-secret")
	calls := 0
	intercept(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "PUT" || r.URL.String() != "https://api.app.shortcut.com/api/v3/stories/456" || r.Header.Get("Shortcut-Token") != "local-test-secret" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"estimate":null}` {
			t.Errorf("lost body: %s", b)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":456,"estimate":null}`)), Header: http.Header{}}, nil
	})
	code, out, err := run(t, "write", "stories", "update", "--story-public-id", "456", "--execute", "--body", `{"estimate":null}`)
	if code != 0 || calls != 1 || !strings.Contains(out, `"safety": "write"`) {
		t.Fatalf("%d calls=%d %s %s", code, calls, out, err)
	}
}
func TestExecuteFailureNotRetried(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "local-test-secret")
	calls := 0
	intercept(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})
	code, out, err := run(t, "write", "stories", "delete", "--story-public-id", "1", "--execute", "--retries", "5")
	if code != 1 || calls != 1 || out != "" {
		t.Fatalf("%d %d %s %s", code, calls, out, err)
	}
}
func TestQueryIsReadOnlyPOST(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "local-test-secret")
	intercept(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/api/v3/stories/search" {
			t.Errorf("bad query request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[]`)), Header: http.Header{}}, nil
	})
	code, out, err := run(t, "--read-only", "read", "stories", "query", "--body", `{"workflow_state_types":["started"]}`)
	if code != 0 || !strings.Contains(out, `"safety": "read"`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestQueryArraySerialization(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "local-test-secret")
	intercept(t, func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query()["entity_types"]; len(got) != 2 || got[0] != "story" || got[1] != "epic" {
			t.Errorf("array encoding %v", got)
		}
		if r.URL.Query().Get("query") != "owner:me" {
			t.Error("missing query")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[],"next":null}`)), Header: http.Header{}}, nil
	})
	code, _, err := run(t, "read", "search", "stories", "--query", "owner:me", "--entity-types", "story", "--entity-types", "epic")
	if code != 0 {
		t.Fatal(err)
	}
}
func TestUpload(t *testing.T) {
	t.Setenv("SHORTCUT_API_TOKEN", "local-test-secret")
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	intercept(t, func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file0")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "sample" || r.FormValue("story_id") != "12" {
			t.Fatal("bad upload")
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`[{"id":1}]`)), Header: http.Header{}}, nil
	})
	code, _, err := run(t, "write", "files", "upload", "--file", path, "--story-id", "12", "--execute")
	if code != 0 {
		t.Fatal(err)
	}
	code, out, err := run(t, "write", "files", "upload", "--file", path, "--dry-run")
	if code != 0 || !strings.Contains(out, `"dry_run": true`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestUploadValidation(t *testing.T) {
	for _, args := range [][]string{{"write", "files", "upload", "--dry-run"}, {"write", "files", "upload", "--dry-run", "--file", "missing-file"}} {
		code, _, _ := run(t, args...)
		if code == 0 {
			t.Fatal("accepted invalid upload")
		}
	}
}
func TestJSONBodyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.json")
	os.WriteFile(path, []byte(`{"name":"hello"}`), 0600)
	code, _, err := run(t, "write", "stories", "update", "--story-public-id", "1", "--dry-run", "--body-file", path)
	if code != 0 {
		t.Fatal(err)
	}
	code, _, _ = run(t, "write", "stories", "update", "--story-public-id", "1", "--dry-run", "--body-file", path, "--body", "{}")
	if code != 2 {
		t.Fatal("accepted conflicting bodies")
	}
}
func TestAllOperationDiscovery(t *testing.T) {
	c, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range c.Operations {
		code, out, err := run(t, op.Safety, op.Resource, op.Command, "--schema")
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatalf("%s %d %s", op.ID, code, err)
		}
	}
}
