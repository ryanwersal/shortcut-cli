package shortcut

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New("test-secret", time.Second, 1024*1024, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.base, _ = url.Parse(server.URL + "/api/v3")
	return c
}
func TestReadAndWriteTransport(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.Header.Get("Shortcut-Token") != "test-secret" || r.URL.Path != "/api/v3/stories/1" {
					t.Errorf("incorrect request")
				}
				if method == "DELETE" {
					w.WriteHeader(204)
				} else {
					fmt.Fprint(w, `{"id":1}`)
				}
			})
			got, err := c.Do(context.Background(), method, "stories/1", nil, "application/json", method == "GET")
			if err != nil || !json.Valid(got) {
				t.Fatalf("%s %v", got, err)
			}
		})
	}
}
func TestStatusErrors(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{{400, 1}, {401, 3}, {403, 3}, {404, 4}, {429, 5}, {500, 1}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, "test-secret") })
			_, err := c.Get(context.Background(), "member")
			var e *Error
			if !errors.As(err, &e) || e.Exit != tc.exit || strings.Contains(e.Message, "test-secret") {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestWritesNeverRetry(t *testing.T) {
	for _, status := range []int{429, 503} {
		calls := 0
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(status)
		})
		c.Retries = 3
		_, err := c.Do(context.Background(), "POST", "stories", []byte(`{}`), "application/json", false)
		if err == nil || calls != 1 {
			t.Fatalf("write sent %d times", calls)
		}
	}
}
func TestReadRetries(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	c.Retries = 2
	if _, err := c.Get(context.Background(), "member"); err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestRedirectDoesNotForwardToken(t *testing.T) {
	received := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true }))
	defer other.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) })
	_, err := c.Get(context.Background(), "member")
	if err == nil || received {
		t.Fatal("followed redirect")
	}
}
func TestRejectUnsafePaths(t *testing.T) {
	c, _ := New("test-secret", time.Second, 1024, 0)
	for _, path := range []string{"https://evil.test/api/v3/member", "//evil.test/api/v3/member", "/api/v4/member", "stories/../member", "stories/%2e%2e/member", "member?token=secret", "member#fragment", "https://u:p@api.app.shortcut.com/api/v3/member"} {
		if _, err := c.resolve(path); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
}
func TestBadResponses(t *testing.T) {
	for _, body := range []string{"not JSON", strings.Repeat("a", 100)} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		c.MaxBytes = 50
		if _, err := c.Get(context.Background(), "member"); err == nil {
			t.Fatal("accepted invalid or oversized response")
		}
	}
}
func TestPaginationResume(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/search/stories" {
			t.Errorf("bad path")
		}
		if r.URL.Query().Get("next") == "two" {
			fmt.Fprint(w, `{"data":[{"id":2}],"next":null}`)
		} else {
			if r.URL.Query().Get("page_size") != "1" {
				t.Error("limit not applied")
			}
			fmt.Fprint(w, `{"data":[{"id":1}],"next":"/api/v3/search/stories?query=test&next=two"}`)
		}
	})
	rows, m, err := c.Search(context.Background(), SearchOptions{Query: "test", Limit: 1, PageSize: 25, MaxPages: 10})
	if err != nil || len(rows) != 1 || !m.Truncated || m.NextCursor == nil {
		t.Fatalf("%+v %v", m, err)
	}
	rows, m, err = c.Search(context.Background(), SearchOptions{Query: "test", Cursor: *m.NextCursor, Limit: 1, PageSize: 25, MaxPages: 10})
	if err != nil || len(rows) != 1 || m.Truncated || string(rows[0]) != `{"id":2}` {
		t.Fatalf("%+v %v", m, err)
	}
}
func TestPaginationFailures(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":[],"next":"/api/v3/search/stories?next=a"}`, `{"data":[{}],"next":"https://evil.test/api/v3/search/stories"}`, `{"data":[{},{}],"next":null}`, `{"data":[{}],"next":"/api/v3/search/stories?next=a"}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		_, _, err := c.Search(context.Background(), SearchOptions{Query: "test", Limit: 10, PageSize: 1, MaxPages: 10})
		if err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}
