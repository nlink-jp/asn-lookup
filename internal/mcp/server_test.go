package mcp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
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
8.8.8.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
`

// pagedCSV gives AS15169 five prefixes, so a page smaller than the AS can be
// walked to the end — the property that replaced writing the list to a file.
const pagedCSV = `network,country,country_code,continent,continent_code,asn,as_name,as_domain
8.8.8.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.9.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.10.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.11.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.12.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
`

const gen = 1720000000

type fakeFetcher struct{ csv string }

func (f fakeFetcher) Fetch(_ context.Context, _, _ string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = io.WriteString(w, f.csv)
	_ = w.Close()
	return io.NopCloser(&buf), nil
}

func newEngine(t *testing.T, writeDB bool) *engine.Engine { return newEngineCSV(t, writeDB, sampleCSV) }

func newEngineCSV(t *testing.T, writeDB bool, csv string) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "asndb.bin")
	if writeDB {
		var buf bytes.Buffer
		if _, _, err := asndb.BuildFromCSV(strings.NewReader(csv), &buf, gen); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dbPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Token:   "tok",
		LiteURL: "https://x.test/lite.csv.gz",
		DBPath:  dbPath,
	}
	e := engine.New(cfg, fakeFetcher{csv: csv})
	e.Now = func() time.Time { return time.Unix(gen, 0) }
	return e
}

// The tools over the module's client: the listing, a lookup each way, and
// the status (the sequence a client runs at start).
func TestToolsOverTheProtocol(t *testing.T) {
	cl := open(t, newEngine(t, true))
	if name := cl.Server().Name; name != "asn-lookup" {
		t.Errorf("serverInfo.name = %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := cl.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 5 {
		t.Errorf("tools = %d, want 5", len(list.Tools))
	}
	if text, isErr := call(t, cl, "lookup_ip", `{"ip":"8.8.8.8"}`); isErr || !strings.Contains(text, "15169") || !strings.Contains(text, `"found": true`) {
		t.Errorf("lookup_ip text = %s (isErr=%v)", text, isErr)
	}
	if text, _ := call(t, cl, "lookup_asn", `{"asn":"AS15169"}`); !strings.Contains(text, "8.8.8.0/24") {
		t.Errorf("lookup_asn text = %s", text)
	}
	if text, _ := call(t, cl, "db_status", ""); !strings.Contains(text, `"records"`) {
		t.Errorf("db_status text = %s", text)
	}
}

func TestToolsCallNoDBHint(t *testing.T) {
	cl := open(t, newEngine(t, false)) // no DB file written
	text, isErr := call(t, cl, "lookup_ip", `{"ip":"8.8.8.8"}`)
	if !isErr || !strings.Contains(text, "update_db") {
		t.Errorf("expected error result hinting update_db, got %s (isErr=%v)", text, isErr)
	}
}

func TestToolUpdateThenStatus(t *testing.T) {
	cl := open(t, newEngine(t, false))
	if text, isErr := call(t, cl, "update_db", ""); isErr || !strings.Contains(text, `"updated": true`) {
		t.Errorf("update_db text = %s (isErr=%v)", text, isErr)
	}
	if text, isErr := call(t, cl, "db_status", ""); isErr || !strings.Contains(text, `"records"`) {
		t.Errorf("db_status after update = %s (isErr=%v)", text, isErr)
	}
}

// Paging is what replaced writing the prefix list to a file: every prefix must
// be reachable, and no path-bearing field may survive in the result.
func TestLookupASNPagingReachesEveryPrefix(t *testing.T) {
	cl := open(t, newEngineCSV(t, true, pagedCSV))
	seen := map[string]bool{}
	for offset := 0; offset < 6; offset += 2 {
		text, isErr := call(t, cl, "lookup_asn", fmt.Sprintf(`{"asn":"AS15169","limit":2,"offset":%d}`, offset))
		if isErr {
			t.Fatalf("offset %d: %s", offset, text)
		}
		for _, gone := range []string{"prefixes_file", "truncated", "preview", "workspace"} {
			if strings.Contains(text, gone) {
				t.Errorf("result still carries %q — file mediation was not removed: %s", gone, text)
			}
		}
		var entries []struct {
			PrefixCount int      `json:"prefix_count"`
			Offset      int      `json:"offset"`
			HasMore     bool     `json:"has_more"`
			Prefixes    []string `json:"prefixes"`
		}
		if err := json.Unmarshal([]byte(text), &entries); err != nil {
			t.Fatalf("offset %d unmarshal: %v (%s)", offset, err, text)
		}
		e0 := entries[0]
		if e0.PrefixCount != 5 {
			t.Fatalf("prefix_count = %d, want the true total 5", e0.PrefixCount)
		}
		if want := offset+2 < 5; e0.HasMore != want {
			t.Errorf("offset %d: has_more = %v, want %v", offset, e0.HasMore, want)
		}
		if len(e0.Prefixes) > 2 {
			t.Errorf("offset %d returned %d prefixes, want at most the limit of 2", offset, len(e0.Prefixes))
		}
		for _, p := range e0.Prefixes {
			seen[p] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("paging reached %d of 5 prefixes: %v", len(seen), seen)
	}
}

// limit:0 means "all of them" — the caller opting out of paging for an AS it
// already knows is small.
func TestLookupASNLimitZeroReturnsEverything(t *testing.T) {
	cl := open(t, newEngineCSV(t, true, pagedCSV))
	text, isErr := call(t, cl, "lookup_asn", `{"asn":"AS15169","limit":0}`)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var entries []struct {
		HasMore  bool     `json:"has_more"`
		Prefixes []string `json:"prefixes"`
	}
	_ = json.Unmarshal([]byte(text), &entries)
	if len(entries[0].Prefixes) != 5 || entries[0].HasMore {
		t.Errorf("limit:0 must inline all 5 prefixes with has_more=false: %+v", entries[0])
	}
}

func TestInitializeInstructionsAndGetUsage(t *testing.T) {
	cl := open(t, newEngine(t, true))
	if !strings.Contains(cl.Instructions(), "get_usage") {
		t.Errorf("initialize instructions should mention get_usage: %q", cl.Instructions())
	}
	text, isErr := call(t, cl, "get_usage", "")
	if isErr || !strings.Contains(text, "Recovery table") || !strings.Contains(text, "offset") {
		t.Errorf("get_usage manual incomplete: isErr=%v", isErr)
	}
}
