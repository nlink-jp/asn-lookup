package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/nlink-jp/mcptool"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/engine"
)

// usageMarkdown is the operating manual returned by the get_usage tool. Its
// coherence with the real tools/results is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Instructions is the hint a client receives when it opens the server (the MCP
// `instructions` field, in initialize or server/discover): it makes get_usage
// discoverable and steers clients away from common errors.
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

// toolsList returns the advertised tool set, in order, with JSON Schema for
// each input. register (below) adds them to the server in this order, and
// the tests read them from here.
func toolsList() []map[string]any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return []map[string]any{
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
	}
}

// register adds every tool of toolsList to ms, in order, each with its
// arguments decoded by mcptool before the handler runs (ADR-0002).
func (s *server) register(ms *mcptool.Server) error {
	handlers := map[string]func(t mcptool.Tool) error{
		"get_usage": func(t mcptool.Tool) error {
			return mcptool.AddNoArgs(ms, t, func(context.Context) (any, error) { return mcptool.Text(usageMarkdown), nil })
		},
		"lookup_ip":  func(t mcptool.Tool) error { return mcptool.Add(ms, t, s.toolLookupIP) },
		"lookup_asn": func(t mcptool.Tool) error { return mcptool.Add(ms, t, s.toolLookupASN) },
		"update_db":  func(t mcptool.Tool) error { return mcptool.AddNoArgs(ms, t, s.toolUpdate) },
		"db_status": func(t mcptool.Tool) error {
			return mcptool.AddNoArgs(ms, t, func(context.Context) (any, error) { return s.toolStatus() })
		},
	}
	for _, def := range toolsList() {
		name, _ := def["name"].(string)
		desc, _ := def["description"].(string)
		schema, err := json.Marshal(def["inputSchema"])
		if err != nil {
			return fmt.Errorf("tool %s: %w", name, err)
		}
		add, ok := handlers[name]
		if !ok {
			return fmt.Errorf("tool %s has no handler", name)
		}
		if err := add(mcptool.Tool{Name: name, Description: desc, InputSchema: schema}); err != nil {
			return fmt.Errorf("tool %s: %w", name, err)
		}
	}
	return nil
}

// ipEntry embeds the found result so its fields inline into the JSON; when the
// address is not found the embedded pointer is nil and only input/found appear.
type ipEntry struct {
	Input string `json:"input"`
	Found bool   `json:"found"`
	Error string `json:"error,omitempty"`
	*asndb.IPResult
}

// lookupIPArgs are lookup_ip's arguments; their names are its schema's.
type lookupIPArgs struct {
	IP  string   `json:"ip"`
	IPs []string `json:"ips"`
}

func (s *server) toolLookupIP(_ context.Context, a lookupIPArgs) (any, error) {
	inputs := a.IPs
	if a.IP != "" {
		inputs = append([]string{a.IP}, inputs...)
	}
	if len(inputs) == 0 {
		return nil, mcptool.NewError(mcptool.CodeMissingArgument, "provide 'ip' (string) or 'ips' (array of strings)").With("one_of", []string{"ip", "ips"})
	}
	db, err := s.database()
	if err != nil {
		return nil, dbError(err)
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
	return entries, nil
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

// lookupASNArgs are lookup_asn's arguments; their names are its schema's.
type lookupASNArgs struct {
	ASN    string   `json:"asn"`
	ASNs   []string `json:"asns"`
	Limit  *int     `json:"limit"`
	Offset *int     `json:"offset"`
}

func (s *server) toolLookupASN(_ context.Context, a lookupASNArgs) (any, error) {
	inputs := a.ASNs
	if a.ASN != "" {
		inputs = append([]string{a.ASN}, inputs...)
	}
	if len(inputs) == 0 {
		return nil, mcptool.NewError(mcptool.CodeMissingArgument, "provide 'asn' (string) or 'asns' (array of strings)").With("one_of", []string{"asn", "asns"})
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
		return nil, dbError(err)
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
	return entries, nil
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func (s *server) toolUpdate(ctx context.Context) (any, error) {
	stats, skipped, err := s.e.Update(ctx)
	if err != nil {
		code := CodeUpdateFailed
		if errors.Is(err, engine.ErrNoToken) {
			code = CodeNoToken
		}
		return nil, mcptool.NewError(code, "update failed: "+err.Error())
	}
	return map[string]any{
		"updated":   true,
		"generated": stats.Generated,
		"records":   stats.RecordCount,
		"v4":        stats.V4Count,
		"v6":        stats.V6Count,
		"skipped":   skipped,
		"path":      s.e.Cfg.DBPath,
	}, nil
}

func (s *server) toolStatus() (any, error) {
	db, err := s.database()
	if err != nil {
		return nil, dbError(err)
	}
	st := db.Stats()
	stale, age := s.e.IsStale(st.Generated)
	return map[string]any{
		"generated": st.Generated,
		"records":   st.RecordCount,
		"v4":        st.V4Count,
		"v6":        st.V6Count,
		"stale":     stale,
		"age_days":  int(age.Hours() / 24),
		"path":      s.e.Cfg.DBPath,
	}, nil
}

// dbError is a database load error as a tool error, with an update hint when
// no database exists yet.
func dbError(err error) error {
	if errors.Is(err, engine.ErrNoDB) {
		return mcptool.NewError(CodeNoDatabase, err.Error()+"\nCall the update_db tool to download the IPinfo Lite database.")
	}
	return mcptool.NewError(CodeDatabaseError, err.Error())
}
