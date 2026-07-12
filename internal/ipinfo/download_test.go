package ipinfo

import (
	"context"
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
		io.WriteString(w, "payload-bytes")
	}))
	defer srv.Close()

	f := NewHTTPFetcher()
	rc, err := f.Fetch(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer rc.Close()
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
