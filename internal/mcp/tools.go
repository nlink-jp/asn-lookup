package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/engine"
	"github.com/nlink-jp/asn-lookup/internal/workspace"
)

// usageMarkdown is the operating manual returned by the get_usage tool. Its
// coherence with the real tools/results is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Instructions is the initialize-time hint (surfaced via the MCP `instructions`
// field) that makes get_usage discoverable and steers clients away from common
// errors.
const Instructions = "asn-lookup answers IP↔AS questions from a local IPinfo Lite database, fully offline. " +
	"Call db_status first; if there is no database, call update_db (an ipinfo token must be configured). " +
	"Large lookup_asn results are file-mediated: pass a writable workspace_root and read the returned prefixes_file. " +
	"Call get_usage for the full tool reference and error-recovery table."

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_usage",
				"description": "Return this server's operating manual (markdown): the tools, the workspace model for file-mediated results, the database lifecycle, and the error-recovery table. Call it once before first use.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name":        "lookup_ip",
				"description": "Look up the AS (ASN, name, domain) and country/continent for one or more IP addresses using the local IPinfo Lite database.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ip":  map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address."},
						"ips": strArray,
					},
				},
			},
			{
				"name": "lookup_asn",
				"description": "List the IP prefixes announced by one or more ASNs (e.g. \"AS15169\" or 15169) from the local IPinfo Lite database. " +
					"Always returns a summary (prefix_count, v4/v6 counts) and an inline preview. Large results are NOT inlined: the full prefix list is written to a file in the workspace and its path is returned (truncated=true). " +
					"To receive that file in a sandboxed environment, create a directory with your own file tools and pass it as workspace_root.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"asn":            map[string]any{"type": "string", "description": "A single ASN, as \"AS15169\" or \"15169\"."},
						"asns":           strArray,
						"limit":          map[string]any{"type": "integer", "description": "Max prefixes to inline before writing a file (default 50)."},
						"format":         map[string]any{"type": "string", "enum": []string{"cidr", "json"}, "description": "Format of the written prefix file (default cidr = one CIDR per line)."},
						"workspace_root": map[string]any{"type": "string", "description": "Absolute path to an agent-prepared directory for the output file; omit to use the server default."},
						"workspace_id":   map[string]any{"type": "string", "description": "Optional single-segment subdirectory under the workspace root."},
					},
				},
			},
			{
				"name":        "update_db",
				"description": "Download the latest IPinfo Lite database and rebuild the local index. Requires an ipinfo token to be configured.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name":        "db_status",
				"description": "Report the local database's generation date, record counts, and whether it is stale.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

func (s *server) toolsCall(ctx context.Context, params json.RawMessage) (toolResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult{}, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	switch p.Name {
	case "get_usage":
		return textResult(false, usageMarkdown), nil
	case "lookup_ip":
		return s.toolLookupIP(p.Arguments), nil
	case "lookup_asn":
		return s.toolLookupASN(p.Arguments), nil
	case "update_db":
		return s.toolUpdate(ctx), nil
	case "db_status":
		return s.toolStatus(), nil
	default:
		return toolResult{}, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
}

// ipEntry embeds the found result so its fields inline into the JSON; when the
// address is not found the embedded pointer is nil and only input/found appear.
type ipEntry struct {
	Input string `json:"input"`
	Found bool   `json:"found"`
	Error string `json:"error,omitempty"`
	*asndb.IPResult
}

func (s *server) toolLookupIP(args json.RawMessage) toolResult {
	var a struct {
		IP  string   `json:"ip"`
		IPs []string `json:"ips"`
	}
	_ = json.Unmarshal(args, &a)
	inputs := a.IPs
	if a.IP != "" {
		inputs = append([]string{a.IP}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'ip' (string) or 'ips' (array of strings)")
	}
	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}
	entries := make([]ipEntry, 0, len(inputs))
	for _, in := range inputs {
		addr, perr := netip.ParseAddr(in)
		if perr != nil {
			entries = append(entries, ipEntry{Input: in, Found: false, Error: "invalid address"})
			continue
		}
		if res, ok := db.LookupIP(addr); ok {
			r := res
			entries = append(entries, ipEntry{Input: in, Found: true, IPResult: &r})
		} else {
			entries = append(entries, ipEntry{Input: in, Found: false})
		}
	}
	return jsonResult(entries)
}

// defaultASNPreview is how many prefixes are inlined before a file is written.
const defaultASNPreview = 50

// asnEntry is the file-mediated reverse-lookup result. Small results inline the
// full Prefixes; large results inline a Preview and write the rest to a file.
type asnEntry struct {
	Input        string   `json:"input"`
	Found        bool     `json:"found"`
	ASN          uint32   `json:"asn"`
	ASName       string   `json:"as_name,omitempty"`
	ASDomain     string   `json:"as_domain,omitempty"`
	PrefixCount  int      `json:"prefix_count"`
	V4Count      int      `json:"v4_count"`
	V6Count      int      `json:"v6_count"`
	Truncated    bool     `json:"truncated"`
	Prefixes     []string `json:"prefixes,omitempty"`
	Preview      []string `json:"preview,omitempty"`
	PrefixesFile string   `json:"prefixes_file,omitempty"`
	Format       string   `json:"format,omitempty"`
	Note         string   `json:"note,omitempty"`
}

func (s *server) toolLookupASN(args json.RawMessage) toolResult {
	var a struct {
		ASN           string   `json:"asn"`
		ASNs          []string `json:"asns"`
		Limit         *int     `json:"limit"`
		Format        string   `json:"format"`
		WorkspaceRoot string   `json:"workspace_root"`
		WorkspaceID   string   `json:"workspace_id"`
	}
	_ = json.Unmarshal(args, &a)

	inputs := a.ASNs
	if a.ASN != "" {
		inputs = append([]string{a.ASN}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'asn' (string) or 'asns' (array of strings)")
	}
	format := a.Format
	if format == "" {
		format = "cidr"
	}
	if format != "cidr" && format != "json" {
		return textResult(true, "format must be \"cidr\" or \"json\"")
	}
	preview := defaultASNPreview
	if a.Limit != nil && *a.Limit >= 0 {
		preview = *a.Limit
	}

	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}

	// Materialize the workspace lazily, only when a file must be written.
	var ws *wsHandle
	entries := make([]asnEntry, 0, len(inputs))
	for _, in := range inputs {
		num, ok := asndb.ParseASN(in)
		if !ok {
			entries = append(entries, asnEntry{Input: in, Found: false})
			continue
		}
		res, found := db.LookupASN(num)
		if !found {
			entries = append(entries, asnEntry{Input: in, Found: false, ASN: num})
			continue
		}
		v4, v6 := res.FamilyCounts()
		all := prefixStrings(res.Prefixes)
		e := asnEntry{
			Input: in, Found: true, ASN: num,
			ASName: res.ASName, ASDomain: res.ASDomain,
			PrefixCount: len(all), V4Count: v4, V6Count: v6,
		}
		if len(all) <= preview {
			e.Prefixes = all
		} else {
			e.Truncated = true
			e.Preview = all[:preview]
			e.Format = format
			if ws == nil {
				ws = s.ensureWorkspace(a.WorkspaceRoot, a.WorkspaceID)
			}
			if ws.err != nil {
				e.Note = "full list not written: " + ws.err.Error() + " — pass a writable 'workspace_root'"
			} else if path, werr := writePrefixFile(ws.ws, num, all, format); werr != nil {
				e.Note = "full list not written: " + werr.Error()
			} else {
				e.PrefixesFile = path
			}
		}
		entries = append(entries, e)
	}
	return jsonResult(entries)
}

// wsHandle memoizes a single workspace materialization (and its error) across
// the ASNs in one call.
type wsHandle struct {
	ws  *workspace.Workspace
	err error
}

func (s *server) ensureWorkspace(root, id string) *wsHandle {
	ws, err := s.ws.EnsureIn(root, id)
	return &wsHandle{ws: ws, err: err}
}

// writePrefixFile writes the full prefix list for an ASN into the workspace and
// returns the absolute path.
func writePrefixFile(ws *workspace.Workspace, asn uint32, prefixes []string, format string) (string, error) {
	var data []byte
	name := fmt.Sprintf("AS%d-prefixes.%s", asn, format)
	if format == "json" {
		b, err := json.MarshalIndent(prefixes, "", "  ")
		if err != nil {
			return "", err
		}
		data = b
	} else {
		data = []byte(strings.Join(prefixes, "\n") + "\n")
	}
	return ws.WriteFileAtomic(name, data)
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func (s *server) toolUpdate(ctx context.Context) toolResult {
	stats, skipped, err := s.e.Update(ctx)
	if err != nil {
		return textResult(true, "update failed: "+err.Error())
	}
	return jsonResult(map[string]any{
		"updated":   true,
		"generated": stats.Generated,
		"records":   stats.RecordCount,
		"v4":        stats.V4Count,
		"v6":        stats.V6Count,
		"skipped":   skipped,
		"path":      s.e.Cfg.DBPath,
	})
}

func (s *server) toolStatus() toolResult {
	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}
	st := db.Stats()
	stale, age := s.e.IsStale(st.Generated)
	return jsonResult(map[string]any{
		"generated": st.Generated,
		"records":   st.RecordCount,
		"v4":        st.V4Count,
		"v6":        st.V6Count,
		"stale":     stale,
		"age_days":  int(age.Hours() / 24),
		"path":      s.e.Cfg.DBPath,
	})
}

// jsonResult marshals v into a non-error text result.
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult(true, "encode result: "+err.Error())
	}
	return textResult(false, string(b))
}

// dbErrorResult renders a database load error, adding an update hint when no
// database exists yet.
func dbErrorResult(err error) toolResult {
	msg := err.Error()
	if errors.Is(err, engine.ErrNoDB) {
		msg += "\nCall the update_db tool to download the IPinfo Lite database."
	}
	return textResult(true, msg)
}
