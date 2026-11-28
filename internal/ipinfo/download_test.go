package ipinfo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"https://ipinfo.io/data/ipinfo_lite.csv.gz?token=secret": "https://ipinfo.io/data/ipinfo_lite.csv.gz?token=REDACTED",
		"https://ipinfo.io/data/ipinfo_lite.csv.gz":              "https://ipinfo.io/data/ipinfo_lite.csv.gz",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithToken(t *testing.T) {
	got, err := withToken("https://example.test/x.gz", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "token=abc") {
		t.Errorf("withToken = %q, want token=abc", got)
	}
	// No token: URL unchanged.
	got, _ = withToken("https://example.test/x.gz", "")
	if got != "https://example.test/x.gz" {
		t.Errorf("withToken(empty) = %q", got)
	}
}

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "tok" {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			http.Error(w, "missing bearer", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "payload-bytes")
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	rc, err := f.Fetch(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if string(body) != "payload-bytes" {
		t.Errorf("body = %q", body)
	}
}

func TestFetchErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	_, err := f.Fetch(context.Background(), srv.URL, "supersecret")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error leaked token: %v", err)
	}
}

// closedURL is the address of a server that has stopped, so a connection to
// it is refused at once (a fixed port might have a listener on it).
func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL + "/lite.csv.gz"
	srv.Close()
	return u
}

// A transport failure (here, a refused local connection) must not carry the
// token: *url.Error holds the whole request URL, query included, and it went
// into update_db's message and the CLI's stderr as it was.
func TestATransportErrorDoesNotCarryTheToken(t *testing.T) {
	u := closedURL(t)
	_, err := NewHTTPFetcher().Fetch(context.Background(), u, "secret-tok-123")
	if err == nil {
		t.Fatal("a refused connection returned no error")
	}
	if strings.Contains(err.Error(), "secret-tok-123") {
		t.Errorf("the token leaked: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "download "+Redact(u)+": ") {
		t.Errorf("the error no longer says where: %v", err)
	}
}

// A cancelled download still reads as a cancellation.
func TestACancelledFetchIsStillACancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewHTTPFetcher().Fetch(ctx, closedURL(t), "secret-tok-123")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret-tok-123") {
		t.Errorf("err = %v", err)
	}
}
