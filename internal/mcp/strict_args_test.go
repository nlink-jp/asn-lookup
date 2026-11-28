package mcp

import (
	"testing"

	"github.com/nlink-jp/mcptool"
)

// Org ADR-021 §4, now enforced by mcptool (ADR-0002): every tool refuses an
// argument it does not declare, naming it, before it reads the database or
// downloads anything. The misspellings below are the dangerous ones: drop
// `limit` or `offset` and a page of a tier-1 AS comes back looking like the
// page asked for, so a walk over its prefixes never ends.
func TestUnknownArgumentIsRefusedByName(t *testing.T) {
	cases := []struct {
		tool  string
		args  string
		field string
	}{
		{"lookup_ip", `{"ip":"8.8.8.8","verbose":true}`, "verbose"},
		{"lookup_asn", `{"asn":"AS15169","offest":50}`, "offest"},
		{"lookup_asn", `{"asn":"AS15169","limit_":10}`, "limit_"},
		{"db_status", `{"verbose":true}`, "verbose"},
		{"update_db", `{"force":true}`, "force"},
		{"get_usage", `{"topic":"paging"}`, "topic"},
		// The schema names are lower case; another case is another name.
		{"lookup_asn", `{"ASN":"AS15169"}`, "ASN"},
		{"lookup_asn", `{"asn":"AS15169","Limit":1}`, "Limit"},
		{"lookup_ip", `{"IP":"8.8.8.8"}`, "IP"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			e := toolErr(t, open(t, newEngine(t, true)), tc.tool, tc.args)
			if e.Code != mcptool.CodeInvalidArguments || e.Details["argument"] != tc.field {
				t.Errorf("%s: %+v, want invalid_arguments naming %q", tc.tool, e, tc.field)
			}
		})
	}
}

// A wrong-typed argument is refused as malformed — not answered as if it
// were absent ("provide 'asn'" would be the wrong answer to a call that did
// provide it).
func TestMalformedArgumentsAreRefused(t *testing.T) {
	cases := []struct {
		name, tool, args, argument string
	}{
		{"number for string", "lookup_ip", `{"ip":8}`, "ip"},
		{"number for string", "lookup_asn", `{"asn":15169}`, "asn"},
		{"string for integer", "lookup_asn", `{"asn":"AS15169","limit":"all"}`, "limit"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.name, func(t *testing.T) {
			e := toolErr(t, open(t, newEngine(t, true)), tc.tool, tc.args)
			if e.Code != mcptool.CodeInvalidArguments || e.Details["argument"] != tc.argument {
				t.Errorf("%s: %+v", tc.tool, e)
			}
		})
	}
}

// Neither ip nor ips (neither asn nor asns) is a missing one-of argument
// (ADR-0002; mcptool's migration policy §2.2).
func TestAMissingOneOfIsMissingArgument(t *testing.T) {
	cl := open(t, newEngine(t, true))
	for tool, want := range map[string][]any{"lookup_ip": {"ip", "ips"}, "lookup_asn": {"asn", "asns"}} {
		e := toolErr(t, cl, tool, `{}`)
		got, _ := e.Details["one_of"].([]any)
		if e.Code != mcptool.CodeMissingArgument || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s: %+v", tool, e)
		}
	}
}

// An omitted, empty or null arguments object still means none.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	cl := open(t, newEngine(t, true))
	for _, args := range []string{``, `{}`, `null`} {
		if text, isErr := call(t, cl, "db_status", args); isErr {
			t.Errorf("db_status with arguments %q was refused: %s", args, text)
		}
	}
}
