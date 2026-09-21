package mcp

import (
	"fmt"
	"strings"
	"testing"
)

// TestUnknownArgumentIsRefusedByName is the enforcing half of org ADR-021 §4:
// additionalProperties:false only tells a client what is allowed, and a client
// that does not check the schema sends the typo anyway. Every tool must refuse
// it, and the message must name the offending field — a caller that is told
// only "invalid arguments" has to re-read the schema to find its own typo.
//
// The misspellings below are the ones that would otherwise be dangerous: drop
// `limit` or `offset` and a page of a tier-1 AS comes back looking like the
// page that was asked for, so a walk over its prefixes silently repeats the
// first 50 and never terminates.
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
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			e := newEngine(t, true)
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tc.tool, tc.args)
			text, isErr := callText(t, drive(t, e, req)[0].Result)
			if !isErr {
				t.Fatalf("%s accepted unknown argument %q: %s", tc.tool, tc.field, text)
			}
			// Matching the decoder's own phrasing, not just the field name:
			// "provide 'hash…'" happens to contain "hash", so a bare substring
			// test passes for the wrong reason. The mutation check caught it.
			want := `unknown field "` + tc.field + `"`
			if !strings.Contains(text, want) {
				t.Errorf("%s: error does not name the offending argument: want %s, got %s", tc.tool, want, text)
			}
		})
	}
}

// TestMalformedArgumentsAreRefused covers the other half of the discarded
// error: `_ = json.Unmarshal` left `a` at its zero value when the object did
// not decode, so a wrong-typed argument produced the same call as an absent
// one — and "provide 'asn'" is a misleading answer to a request that did
// provide it.
func TestMalformedArgumentsAreRefused(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{"number for string", "lookup_ip", `{"ip":8}`},
		{"array for object", "lookup_ip", `["8.8.8.8"]`},
		{"number for string", "lookup_asn", `{"asn":15169}`},
		{"string for integer", "lookup_asn", `{"asn":"AS15169","limit":"all"}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.name, func(t *testing.T) {
			e := newEngine(t, true)
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tc.tool, tc.args)
			text, isErr := callText(t, drive(t, e, req)[0].Result)
			if !isErr {
				t.Fatalf("%s accepted malformed arguments: %s", tc.tool, text)
			}
			if strings.Contains(text, "provide '") {
				t.Errorf("%s reported the argument as missing instead of malformed: %s", tc.tool, text)
			}
			if !strings.Contains(text, "arguments:") {
				t.Errorf("%s: error is not a decode error: %s", tc.tool, text)
			}
		})
	}
}

// TestOmittedArgumentsStillMeanNone pins the boundary of the change: strict
// decoding must not turn a legitimately argument-less call into an error.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	for _, args := range []string{``, `,"arguments":{}`, `,"arguments":null`} {
		e := newEngine(t, true)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"db_status"%s}}`, args)
		text, isErr := callText(t, drive(t, e, req)[0].Result)
		if isErr {
			t.Errorf("db_status with arguments %q was refused: %s", args, text)
		}
	}
}
