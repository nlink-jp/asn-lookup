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

// rawResp is a partial JSON-RPC response for assertions.
type rawResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func drive(t *testing.T, e *engine.Engine, requests ...string) []rawResp {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), e, "test-ver", in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rawResp
	dec := json.NewDecoder(&out)
	for {
		var r rawResp
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode response: %v (buffer: %s)", err, out.String())
		}
		resps = append(resps, r)
	}
	return resps
}

// callText extracts the text content of a tools/call result.
func callText(t *testing.T, result json.RawMessage) (string, bool) {
	t.Helper()
	var tr toolResult
	if err := json.Unmarshal(result, &tr); err != nil {
		t.Fatalf("unmarshal toolResult: %v", err)
	}
	if len(tr.Content) == 0 {
		t.Fatal("empty content")
	}
	return tr.Content[0].Text, tr.IsError
}

func TestServeSequence(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no response expected
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"lookup_ip","arguments":{"ip":"8.8.8.8"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"lookup_asn","arguments":{"asn":"AS15169"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"db_status"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
	)
	if len(resps) != 6 {
		t.Fatalf("got %d responses, want 6 (notification must be silent)", len(resps))
	}

	// initialize
	var initRes struct {
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	_ = json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ServerInfo.Name != "asn-lookup" {
		t.Errorf("serverInfo.name = %q", initRes.ServerInfo.Name)
	}

	// tools/list
	var listRes struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(resps[1].Result, &listRes)
	if len(listRes.Tools) != 5 {
		t.Errorf("tools = %d, want 5", len(listRes.Tools))
	}

	// lookup_ip
	text, isErr := callText(t, resps[2].Result)
	if isErr || !strings.Contains(text, "15169") || !strings.Contains(text, `"found": true`) {
		t.Errorf("lookup_ip text = %s (isErr=%v)", text, isErr)
	}

	// lookup_asn
	text, _ = callText(t, resps[3].Result)
	if !strings.Contains(text, "8.8.8.0/24") {
		t.Errorf("lookup_asn text = %s", text)
	}

	// db_status
	text, _ = callText(t, resps[4].Result)
	if !strings.Contains(text, `"records"`) {
		t.Errorf("db_status text = %s", text)
	}
}

func TestToolsCallNoDBHint(t *testing.T) {
	e := newEngine(t, false) // no DB file written
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_ip","arguments":{"ip":"8.8.8.8"}}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if !isErr || !strings.Contains(text, "update_db") {
		t.Errorf("expected error result hinting update_db, got %s (isErr=%v)", text, isErr)
	}
}

func TestToolUpdateThenStatus(t *testing.T) {
	e := newEngine(t, false)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_db"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"db_status"}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if isErr || !strings.Contains(text, `"updated": true`) {
		t.Errorf("update_db text = %s (isErr=%v)", text, isErr)
	}
	text, isErr = callText(t, resps[1].Result)
	if isErr || !strings.Contains(text, `"records"`) {
		t.Errorf("db_status after update = %s (isErr=%v)", text, isErr)
	}
}

// Paging is what replaced writing the prefix list to a file: every prefix must
// be reachable, and no path-bearing field may survive in the result.
func TestLookupASNPagingReachesEveryPrefix(t *testing.T) {
	e := newEngineCSV(t, true, pagedCSV)
	seen := map[string]bool{}
	for offset := 0; offset < 6; offset += 2 {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_asn","arguments":{"asn":"AS15169","limit":2,"offset":%d}}}`, offset)
		text, isErr := callText(t, drive(t, e, req)[0].Result)
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
	e := newEngineCSV(t, true, pagedCSV)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_asn","arguments":{"asn":"AS15169","limit":0}}}`
	text, isErr := callText(t, drive(t, e, req)[0].Result)
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
	e := newEngine(t, true)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_usage"}}`,
	)
	var init struct {
		Instructions string `json:"instructions"`
	}
	_ = json.Unmarshal(resps[0].Result, &init)
	if !strings.Contains(init.Instructions, "get_usage") {
		t.Errorf("initialize instructions should mention get_usage: %q", init.Instructions)
	}
	text, isErr := callText(t, resps[1].Result)
	if isErr || !strings.Contains(text, "Recovery table") || !strings.Contains(text, "offset") {
		t.Errorf("get_usage manual incomplete: isErr=%v", isErr)
	}
}

func TestUnknownMethod(t *testing.T) {
	e := newEngine(t, true)
	resps := drive(t, e, `{"jsonrpc":"2.0","id":9,"method":"bogus/method"}`)
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Errorf("expected -32601 method not found, got %+v", resps[0].Error)
	}
}
