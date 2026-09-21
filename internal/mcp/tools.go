package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/netip"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/engine"
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
	"lookup_asn returns prefixes inline, a page at a time: walk a large AS with limit + offset. " +
	"Call get_usage for the full tool reference and error-recovery table."

// obj builds a tool's input schema. Every schema goes through here so that
// org ADR-021 §10's `additionalProperties: false` is set once instead of being
// remembered per tool — the next tool added gets the closed schema for free.
func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_usage",
				"description": "Return this server's operating manual (markdown): the tools, prefix paging, the database lifecycle, and the error-recovery table. Call it once before first use.",
				"inputSchema": obj(map[string]any{}),
			},
			{
				"name":        "lookup_ip",
				"description": "Look up the AS (ASN, name, domain) and country/continent for one or more IP addresses using the local IPinfo Lite database.",
				"inputSchema": obj(map[string]any{
					"ip":  map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address."},
					"ips": strArray,
				}),
			},
			{
				"name": "lookup_asn",
				"description": "List the IP prefixes announced by one or more ASNs (e.g. \"AS15169\" or 15169) from the local IPinfo Lite database. " +
					"Always returns a summary (prefix_count, v4/v6 counts) and one page of prefixes inline. " +
					"A large AS holds thousands of prefixes, so the page is bounded by limit (default 50) and offset walks the rest; has_more says whether any are left.",
				"inputSchema": obj(map[string]any{
					"asn":    map[string]any{"type": "string", "description": "A single ASN, as \"AS15169\" or \"15169\"."},
					"asns":   strArray,
					"limit":  map[string]any{"type": "integer", "description": "Prefixes per page (default 50). 0 means all of them — only safe for an AS you already know is small."},
					"offset": map[string]any{"type": "integer", "description": "0-based index of the first prefix to return (default 0). Walk a large AS by adding limit each call while has_more is true."},
				}),
			},
			{
				"name":        "update_db",
				"description": "Download the latest IPinfo Lite database and rebuild the local index. Requires an ipinfo token to be configured.",
				"inputSchema": obj(map[string]any{}),
			},
			{
				"name":        "db_status",
				"description": "Report the local database's generation date, record counts, and whether it is stale.",
				"inputSchema": obj(map[string]any{}),
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

// defaultASNPageSize bounds one page of prefixes. A tier-1 AS holds thousands,
// and an unbounded default would put all of them in a model's context on the
// first, most naive call.
const defaultASNPageSize = 50

// asnEntry is one page of a reverse lookup. The whole prefix list is reachable
// by paging: PrefixCount is the total, Offset/Limit say what this page covers,
// and HasMore says whether to ask again. Nothing is written to disk.
type asnEntry struct {
	Input       string   `json:"input"`
	Found       bool     `json:"found"`
	ASN         uint32   `json:"asn"`
	ASName      string   `json:"as_name,omitempty"`
	ASDomain    string   `json:"as_domain,omitempty"`
	PrefixCount int      `json:"prefix_count"`
	V4Count     int      `json:"v4_count"`
	V6Count     int      `json:"v6_count"`
	Offset      int      `json:"offset"`
	Limit       int      `json:"limit"`
	HasMore     bool     `json:"has_more"`
	Prefixes    []string `json:"prefixes"`
}

func (s *server) toolLookupASN(args json.RawMessage) toolResult {
	var a struct {
		ASN    string   `json:"asn"`
		ASNs   []string `json:"asns"`
		Limit  *int     `json:"limit"`
		Offset *int     `json:"offset"`
	}
	_ = json.Unmarshal(args, &a)

	inputs := a.ASNs
	if a.ASN != "" {
		inputs = append([]string{a.ASN}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'asn' (string) or 'asns' (array of strings)")
	}
	limit := defaultASNPageSize
	if a.Limit != nil && *a.Limit >= 0 {
		limit = *a.Limit
	}
	offset := 0
	if a.Offset != nil && *a.Offset > 0 {
		offset = *a.Offset
	}

	db, err := s.database()
	if err != nil {
		return dbErrorResult(err)
	}

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
			Offset: offset, Limit: limit, Prefixes: []string{},
		}
		if offset < len(all) {
			end := len(all)
			if limit > 0 && offset+limit < end {
				end = offset + limit
			}
			e.Prefixes = all[offset:end]
			e.HasMore = end < len(all)
		}
		entries = append(entries, e)
	}
	return jsonResult(entries)
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
