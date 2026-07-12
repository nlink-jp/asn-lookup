// Package ipinfo fetches the IPinfo Lite database download.
//
// The token authenticates to the user's own ipinfo account. It is sent both as
// an Authorization: Bearer header and (as the endpoint documents) a query
// parameter; the query form is redacted whenever a URL is surfaced in errors or
// logs so the secret never leaks.
package ipinfo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Fetcher retrieves the Lite download as a stream. Implementations must return
// a ReadCloser the caller is responsible for closing.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL, token string) (io.ReadCloser, error)
}

// HTTPFetcher is the production Fetcher.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher with a sane default timeout.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{Client: &http.Client{Timeout: 10 * time.Minute}}
}

// Fetch performs the authenticated GET. On non-200 responses it returns an
// error with the token redacted.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL, token string) (io.ReadCloser, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	full, err := withToken(rawURL, token)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", Redact(rawURL), err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d: %s", Redact(rawURL), resp.StatusCode, trimBody(body))
	}
	return resp.Body, nil
}

// withToken appends the token as a query parameter.
func withToken(rawURL, token string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if token != "" {
		q := u.Query()
		q.Set("token", token)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// Redact returns rawURL with any token query parameter masked, safe for logs.
func Redact(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	if q.Get("token") != "" {
		q.Set("token", "REDACTED")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func trimBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
