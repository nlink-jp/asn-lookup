package mcp

import (
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
		// encoding/json matches names case-insensitively; the schema does not.
		{"lookup_asn", `{"ASN":"AS15169"}`, "ASN"},
		{"lookup_asn", `{"asn":"AS15169","Limit":1}`, "Limit"},
		{"lookup_ip", `{"IP":"8.8.8.8"}`, "IP"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			cl := open(t, newEngine(t, true))
			text, isErr := call(t, cl, tc.tool, tc.args)
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
		{"number for string", "lookup_asn", `{"asn":15169}`},
		{"string for integer", "lookup_asn", `{"asn":"AS15169","limit":"all"}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.name, func(t *testing.T) {
			cl := open(t, newEngine(t, true))
			text, isErr := call(t, cl, tc.tool, tc.args)
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

// Arguments that are not an object never reach a tool: the protocol layer
// refuses them with -32602 (server-migration §4), before decodeArgs.
func TestNonObjectArgumentsAreAProtocolError(t *testing.T) {
	cl := open(t, newEngine(t, true))
	if _, err := callErr(t, cl, "lookup_ip", `["8.8.8.8"]`); rpcCode(err) != -32602 {
		t.Errorf("array arguments: err = %v, want -32602", err)
	}
}

// TestOmittedArgumentsStillMeanNone pins the boundary of the change: strict
// decoding must not turn a legitimately argument-less call into an error.
// Over the protocol an omitted or null `arguments` reaches the tool as {} (the
// module and its client replace it); decodeArgs's own empty and null branch
// covers a caller that hands it raw bytes.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	cl := open(t, newEngine(t, true))
	for _, args := range []string{``, `{}`, `null`} {
		if text, isErr := call(t, cl, "db_status", args); isErr {
			t.Errorf("db_status with arguments %q was refused: %s", args, text)
		}
	}
}
