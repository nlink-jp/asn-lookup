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
	cfg := &config.Config{
		Token:     "tok",
		LiteURL:   "https://x.test/lite.csv.gz",
		DBPath:    dbPath,
		Workspace: filepath.Join(t.TempDir(), "ws"),
	}
	e := engine.New(cfg, fakeFetcher{csv: sampleCSV})
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
	json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ServerInfo.Name != "asn-lookup" {
		t.Errorf("serverInfo.name = %q", initRes.ServerInfo.Name)
	}

	// tools/list
	var listRes struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	json.Unmarshal(resps[1].Result, &listRes)
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

func TestLookupASNWritesFileWhenLarge(t *testing.T) {
	e := newEngine(t, true)
	wsRoot := t.TempDir()
	// limit:0 forces every found ASN over the inline threshold.
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_asn","arguments":{"asn":"AS15169","limit":0,"format":"cidr","workspace_root":%q}}}`, wsRoot)
	resps := drive(t, e, req)
	text, isErr := callText(t, resps[0].Result)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("unmarshal entries: %v (%s)", err, text)
	}
	e0 := entries[0]
	if e0["truncated"] != true {
		t.Errorf("expected truncated=true: %v", e0)
	}
	pf, _ := e0["prefixes_file"].(string)
	if pf == "" || !strings.HasPrefix(pf, wsRoot) {
		t.Fatalf("prefixes_file %q not under workspace %q", pf, wsRoot)
	}
	data, err := os.ReadFile(pf)
	if err != nil {
		t.Fatalf("read prefixes_file: %v", err)
	}
	if !strings.Contains(string(data), "8.8.8.0/24") {
		t.Errorf("file content = %q", data)
	}
}

func TestLookupASNBadWorkspaceGivesNote(t *testing.T) {
	e := newEngine(t, true)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup_asn","arguments":{"asn":"AS15169","limit":0,"workspace_root":"relative/not/absolute"}}}`
	resps := drive(t, e, req)
	text, _ := callText(t, resps[0].Result)
	var entries []map[string]any
	json.Unmarshal([]byte(text), &entries)
	e0 := entries[0]
	if _, ok := e0["prefixes_file"]; ok {
		t.Errorf("should not have written a file: %v", e0)
	}
	note, _ := e0["note"].(string)
	if !strings.Contains(note, "workspace_root") {
		t.Errorf("expected note about workspace_root, got %q", note)
	}
	// The lookup itself still succeeds with a preview + count.
	if e0["prefix_count"].(float64) != 1 {
		t.Errorf("prefix_count = %v", e0["prefix_count"])
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
	json.Unmarshal(resps[0].Result, &init)
	if !strings.Contains(init.Instructions, "get_usage") {
		t.Errorf("initialize instructions should mention get_usage: %q", init.Instructions)
	}
	text, isErr := callText(t, resps[1].Result)
	if isErr || !strings.Contains(text, "Recovery table") || !strings.Contains(text, "workspace_root") {
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
