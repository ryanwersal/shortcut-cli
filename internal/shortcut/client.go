package shortcut

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Endpoint = "https://api.app.shortcut.com/api/v3"

type Client struct {
	base     *url.URL
	token    string
	http     *http.Client
	MaxBytes int64
	Retries  int
}

func New(token string, timeout time.Duration, maxBytes int64, retries int) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, Fail("configuration_error", 2, "set SHORTCUT_API_TOKEN")
	}
	if timeout <= 0 || maxBytes < 1 || maxBytes > 50*1024*1024 || retries < 0 || retries > 5 {
		return nil, Fail("invalid_input", 2, "timeout must be positive, max-response-bytes 1..52428800, and retries 0..5")
	}
	base, _ := url.Parse(Endpoint)
	return &Client{base, token, &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, maxBytes, retries}, nil
}

// resolve accepts API-relative paths and same-endpoint pagination links only.
func (c *Client) resolve(path string) (*url.URL, error) {
	u, err := url.Parse(path)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, Fail("invalid_input", 2, "invalid API path")
	}
	if !u.IsAbs() && u.Host == "" && !strings.HasPrefix(u.Path, "/api/") {
		u.Path = c.base.Path + "/" + strings.TrimPrefix(u.Path, "/")
	}
	if u.Host == "" {
		u.Scheme = c.base.Scheme
		u.Host = c.base.Host
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host || !strings.HasPrefix(u.Path, c.base.Path+"/") || strings.Contains(u.Path, "\\") {
		return nil, Fail("invalid_input", 2, "path must stay within the Shortcut v3 API")
	}
	for _, s := range strings.Split(u.Path, "/") {
		if s == ".." || s == "." {
			return nil, Fail("invalid_input", 2, "path traversal is not allowed")
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, Fail("invalid_input", 2, "invalid query string")
	}
	for k := range q {
		if strings.EqualFold(k, "token") {
			return nil, Fail("invalid_input", 2, "tokens must be supplied through SHORTCUT_API_TOKEN")
		}
	}
	return u, nil
}
func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.Do(ctx, http.MethodGet, path, nil, "application/json", true)
}

// Do retries only operations explicitly classified as reads. Writes are sent once.
func (c *Client) Do(ctx context.Context, method, path string, payload []byte, contentType string, readOnly bool) (json.RawMessage, error) {
	u, err := c.resolve(path)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(payload))
		if err != nil {
			return nil, Fail("invalid_input", 2, "invalid request")
		}
		req.Header.Set("Shortcut-Token", c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", "shortcut-cli")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, Fail("transport_error", 5, "Shortcut request failed (check connection or timeout)")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, c.MaxBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, Fail("transport_error", 5, "could not read response")
		}
		if int64(len(body)) > c.MaxBytes {
			return nil, Fail("invalid_response", 1, "response exceeds max-response-bytes")
		}
		if (resp.StatusCode == 429 || resp.StatusCode >= 500) && readOnly && attempt < c.Retries {
			delay := time.Second * time.Duration(1<<attempt)
			if v := resp.Header.Get("Retry-After"); v != "" {
				if d, e := time.ParseDuration(v + "s"); e == nil {
					delay = d
				} else if t, e := http.ParseTime(v); e == nil {
					delay = time.Until(t)
				}
			}
			if delay < 0 {
				delay = 0
			}
			if delay > 30*time.Second {
				return nil, statusError(resp.StatusCode)
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, Fail("transport_error", 5, "request canceled")
			case <-timer.C:
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, statusError(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNoContent {
			return json.RawMessage("null"), nil
		}
		if !json.Valid(body) {
			return nil, Fail("invalid_response", 1, "Shortcut returned invalid JSON")
		}
		// Never echo credentials, even if an upstream response includes them.
		body = bytes.ReplaceAll(body, []byte(c.token), []byte("[REDACTED]"))
		if !json.Valid(body) {
			return nil, Fail("invalid_response", 1, "could not safely render response")
		}
		return body, nil
	}
}
func statusError(status int) error {
	code, exit := "api_error", 1
	switch status {
	case 401:
		code, exit = "authentication_failed", 3
	case 403:
		code, exit = "forbidden", 3
	case 404:
		code, exit = "not_found", 4
	case 429:
		code, exit = "rate_limited", 5
	}
	return Fail(code, exit, "Shortcut returned HTTP %d", status)
}

type SearchOptions struct {
	Query, Cursor             string
	Limit, PageSize, MaxPages int
}
type Meta struct {
	Count      int     `json:"count"`
	Truncated  bool    `json:"truncated"`
	NextCursor *string `json:"next_cursor"`
	Requests   int     `json:"requests"`
}

func (c *Client) Search(ctx context.Context, o SearchOptions) ([]json.RawMessage, Meta, error) {
	rows := []json.RawMessage{}
	m := Meta{}
	if strings.TrimSpace(o.Query) == "" || o.Limit < 1 || o.Limit > 1000 || o.PageSize < 1 || o.PageSize > 250 || o.MaxPages < 1 || o.MaxPages > 1000 {
		return nil, m, Fail("invalid_input", 2, "query required; limit 1..1000, page-size 1..250, max-pages 1..1000")
	}
	next := o.Cursor
	seen := map[string]bool{}
	total := 0
	for {
		size := min(o.PageSize, o.Limit-len(rows))
		var u *url.URL
		var err error
		if next == "" {
			u, err = c.resolve("search/stories")
			if err == nil {
				q := u.Query()
				q.Set("query", o.Query)
				u.RawQuery = q.Encode()
			}
		} else {
			u, err = c.resolve(next)
		}
		if err != nil {
			return nil, m, err
		}
		if u.Path != c.base.Path+"/search/stories" {
			return nil, m, Fail("invalid_input", 2, "cursor must refer to story search")
		}
		q := u.Query()
		q.Set("page_size", jsonNumber(size))
		u.RawQuery = q.Encode()
		if seen[u.String()] {
			return nil, m, Fail("invalid_response", 1, "pagination cursor repeated")
		}
		seen[u.String()] = true
		body, err := c.Get(ctx, u.String())
		if err != nil {
			return nil, m, err
		}
		total += len(body)
		if total > 50*1024*1024 {
			return nil, m, Fail("invalid_response", 1, "combined search response exceeds 50 MiB")
		}
		var page struct {
			Data []json.RawMessage `json:"data"`
			Next *string           `json:"next"`
		}
		if json.Unmarshal(body, &page) != nil || page.Data == nil || len(page.Data) > size {
			return nil, m, Fail("invalid_response", 1, "invalid search page")
		}
		rows = append(rows, page.Data...)
		m.Requests++
		m.Count = len(rows)
		m.NextCursor = page.Next
		if page.Next == nil || *page.Next == "" {
			m.NextCursor = nil
			return rows, m, nil
		}
		if len(page.Data) == 0 {
			return nil, m, Fail("invalid_response", 1, "empty search page has a next cursor")
		}
		// Validate before exposing a server-supplied continuation to callers.
		nextURL, err := c.resolve(*page.Next)
		if err != nil || nextURL.Path != c.base.Path+"/search/stories" {
			return nil, m, Fail("invalid_response", 1, "unsafe search continuation")
		}
		if len(rows) >= o.Limit || m.Requests >= o.MaxPages {
			m.Truncated = true
			return rows, m, nil
		}
		next = *page.Next
	}
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
