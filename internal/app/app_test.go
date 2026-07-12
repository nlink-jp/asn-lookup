package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/config"
	"github.com/nlink-jp/asn-lookup/internal/engine"
)

const sampleCSV = `network,country,country_code,continent,continent_code,asn,as_name,as_domain
1.1.1.0/24,Australia,AU,Oceania,OC,AS13335,"Cloudflare, Inc.",cloudflare.com
8.8.8.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.4.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
2606:4700::/32,,,,,AS13335,"Cloudflare, Inc.",cloudflare.com
203.0.113.0/24,,,,,,,
`

const gen = 1720000000

type fakeFetcher struct{ csv string }

func (f fakeFetcher) Fetch(_ context.Context, _, _ string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	io.WriteString(w, f.csv)
	w.Close()
	return io.NopCloser(&buf), nil
}

func newEngine(t *testing.T, writeDB bool) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "asndb.bin")
	if writeDB {
		var buf bytes.Buffer
		if _, _, err := asndb.BuildFromCSV(strings.NewReader(sampleCSV), &buf, gen); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dbPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Token: "tok", LiteURL: "https://x.test/lite.csv.gz", DBPath: dbPath}
	e := engine.New(cfg, fakeFetcher{csv: sampleCSV})
	e.Now = func() time.Time { return time.Unix(gen, 0) }
	return e
}

func TestRunIPTable(t *testing.T) {
	e := newEngine(t, true)
	var out, errw bytes.Buffer
	code := runIP(&out, &errw, strings.NewReader(""), e, false,
		[]string{"8.8.8.8", "203.0.113.7", "192.0.2.9", "bogus"})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	s := out.String()
	for _, want := range []string{"AS15169", "Google LLC", "8.8.8.0/24", "(not found)", "(invalid)"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

func TestRunIPJSON(t *testing.T) {
	e := newEngine(t, true)
	var out, errw bytes.Buffer
	runIP(&out, &errw, strings.NewReader(""), e, true, []string{"8.8.8.8", "bogus", "192.0.2.9"})

	var lines []map[string]any
	dec := json.NewDecoder(&out)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode: %v", err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d JSON lines, want 3", len(lines))
	}
	if lines[0]["found"] != true || lines[0]["asn"].(float64) != 15169 {
		t.Errorf("line0 = %v", lines[0])
	}
	if lines[1]["found"] != false || lines[1]["error"] != "invalid address" {
		t.Errorf("line1 = %v", lines[1])
	}
	if lines[2]["found"] != false {
		t.Errorf("line2 = %v", lines[2])
	}
}

func TestRunIPNoDB(t *testing.T) {
	e := newEngine(t, false)
	var out, errw bytes.Buffer
	code := runIP(&out, &errw, strings.NewReader(""), e, false, []string{"8.8.8.8"})
	if code != 1 || !strings.Contains(errw.String(), "update") {
		t.Errorf("code=%d err=%q", code, errw.String())
	}
}

func TestRunASN(t *testing.T) {
	e := newEngine(t, true)
	var out, errw bytes.Buffer
	code := runASN(&out, &errw, strings.NewReader(""), e, false, false, 0,
		[]string{"AS15169", "AS13335", "999999", "bad"})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	s := out.String()
	for _, want := range []string{"8.8.4.0/24", "8.8.8.0/24", "2606:4700::/32", "(not found)"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(errw.String(), "invalid ASN") {
		t.Errorf("expected invalid ASN warning, got %q", errw.String())
	}
}

func TestRunASNCountAndLimit(t *testing.T) {
	e := newEngine(t, true)

	// --count: header with counts, no prefix lines.
	var out bytes.Buffer
	runASN(&out, io.Discard, strings.NewReader(""), e, false, true, 0, []string{"AS15169"})
	s := out.String()
	if !strings.Contains(s, "v4 2, v6 0") {
		t.Errorf("count header missing family counts:\n%s", s)
	}
	if strings.Contains(s, "8.8.8.0/24") {
		t.Errorf("--count should not list prefixes:\n%s", s)
	}

	// --limit 1: one prefix + truncation note.
	out.Reset()
	runASN(&out, io.Discard, strings.NewReader(""), e, false, false, 1, []string{"AS15169"})
	s = out.String()
	if !strings.Contains(s, "showing 1 of 2") {
		t.Errorf("expected truncation note, got:\n%s", s)
	}

	// JSON --count: summary only, no prefixes.
	out.Reset()
	runASN(&out, io.Discard, strings.NewReader(""), e, true, true, 0, []string{"AS15169"})
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	if m["prefix_count"].(float64) != 2 || m["v4_count"].(float64) != 2 {
		t.Errorf("counts = %v", m)
	}
	if _, ok := m["prefixes"]; ok {
		t.Errorf("--count JSON should omit prefixes: %v", m)
	}
}

func TestReadInputsStdin(t *testing.T) {
	got := readInputs(nil, strings.NewReader("8.8.8.8 1.1.1.1\n# comment\n\n2.2.2.2\n"))
	want := []string{"8.8.8.8", "1.1.1.1", "2.2.2.2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Args take precedence over stdin.
	if g := readInputs([]string{"9.9.9.9"}, strings.NewReader("1.1.1.1")); len(g) != 1 || g[0] != "9.9.9.9" {
		t.Errorf("args precedence broken: %v", g)
	}
}

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	// Flags appear before, between, and after positionals.
	pos, err := parseInterspersed(fs, []string{"8.8.8.8", "--db", "/tmp/x.bin", "1.1.1.1", "-c", "/tmp/c.toml"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.db != "/tmp/x.bin" || c.config != "/tmp/c.toml" {
		t.Errorf("flags not parsed: db=%q config=%q", c.db, c.config)
	}
	want := []string{"8.8.8.8", "1.1.1.1"}
	if len(pos) != 2 || pos[0] != want[0] || pos[1] != want[1] {
		t.Errorf("positionals = %v, want %v", pos, want)
	}
}

func TestRunUpdate(t *testing.T) {
	e := newEngine(t, false)
	var out, errw bytes.Buffer
	if code := runUpdate(&out, &errw, e); code != 0 {
		t.Fatalf("code = %d, err=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "updated") {
		t.Errorf("stdout = %q", out.String())
	}
	// The database is now usable.
	if _, err := e.LoadDB(); err != nil {
		t.Errorf("LoadDB after update: %v", err)
	}
}

func TestRunDoctor(t *testing.T) {
	e := newEngine(t, true)
	var out bytes.Buffer
	if code := runDoctor(&out, e, "/nonexistent/config.toml"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "status:    OK") || !strings.Contains(s, "token:       configured") {
		t.Errorf("doctor output:\n%s", s)
	}

	// Missing DB → non-zero exit.
	e2 := newEngine(t, false)
	var out2 bytes.Buffer
	if code := runDoctor(&out2, e2, ""); code != 1 {
		t.Errorf("missing-db doctor code = %d, want 1", code)
	}
}
