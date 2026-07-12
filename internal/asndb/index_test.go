package asndb

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"
)

// sampleCSV mirrors the IPinfo Lite schema (columns resolved by header name).
// 8.8.x rows share one AS record (dedup); Cloudflare v4 and v6 differ by
// country so they are distinct records; the last row has no ASN.
const sampleCSV = `network,country,country_code,continent,continent_code,asn,as_name,as_domain
1.1.1.0/24,Australia,AU,Oceania,OC,AS13335,"Cloudflare, Inc.",cloudflare.com
8.8.8.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
8.8.4.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
2606:4700::/32,,,,,AS13335,"Cloudflare, Inc.",cloudflare.com
203.0.113.0/24,,,,,,,
`

const testGenerated = 1720000000 // fixed; Build must not read the clock

func buildTestDB(t *testing.T) *DB {
	t.Helper()
	var buf bytes.Buffer
	st, skipped, err := BuildFromCSV(strings.NewReader(sampleCSV), &buf, testGenerated)
	if err != nil {
		t.Fatalf("BuildFromCSV: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if st.V4Count != 4 || st.V6Count != 1 {
		t.Fatalf("counts v4=%d v6=%d, want 4/1", st.V4Count, st.V6Count)
	}
	if st.RecordCount != 4 {
		t.Fatalf("RecordCount = %d, want 4 (Cloudflare-AU, Google, Cloudflare-empty, empty)", st.RecordCount)
	}
	db, err := Open(buf.Bytes())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func TestLookupIP(t *testing.T) {
	db := buildTestDB(t)

	tests := []struct {
		ip       string
		wantFind bool
		asn      uint32
		name     string
		network  string
		country  string
	}{
		{"1.1.1.1", true, 13335, "Cloudflare, Inc.", "1.1.1.0/24", "Australia"},
		{"8.8.8.8", true, 15169, "Google LLC", "8.8.8.0/24", "United States"},
		{"8.8.4.4", true, 15169, "Google LLC", "8.8.4.0/24", "United States"},
		{"2606:4700::1111", true, 13335, "Cloudflare, Inc.", "2606:4700::/32", ""},
		{"203.0.113.7", true, 0, "", "203.0.113.0/24", ""},                           // present but no ASN
		{"192.0.2.1", false, 0, "", "", ""},                                          // gap between known ranges
		{"9.9.9.9", false, 0, "", "", ""},                                            // above all v4 ranges
		{"::ffff:8.8.8.8", true, 15169, "Google LLC", "8.8.8.0/24", "United States"}, // v4-in-v6 normalizes
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			got, ok := db.LookupIP(netip.MustParseAddr(tc.ip))
			if ok != tc.wantFind {
				t.Fatalf("found = %v, want %v", ok, tc.wantFind)
			}
			if !ok {
				return
			}
			if got.ASN != tc.asn {
				t.Errorf("ASN = %d, want %d", got.ASN, tc.asn)
			}
			if got.ASName != tc.name {
				t.Errorf("ASName = %q, want %q", got.ASName, tc.name)
			}
			if got.Network.String() != tc.network {
				t.Errorf("Network = %s, want %s", got.Network, tc.network)
			}
			if got.Country != tc.country {
				t.Errorf("Country = %q, want %q", got.Country, tc.country)
			}
		})
	}
}

func TestLookupASN(t *testing.T) {
	db := buildTestDB(t)

	t.Run("google v4 only", func(t *testing.T) {
		res, ok := db.LookupASN(15169)
		if !ok {
			t.Fatal("expected found")
		}
		if res.ASName != "Google LLC" || res.ASDomain != "google.com" {
			t.Errorf("meta = %q/%q", res.ASName, res.ASDomain)
		}
		got := prefixStrings(res.Prefixes)
		want := []string{"8.8.4.0/24", "8.8.8.0/24"} // sorted ascending
		if !equalStrings(got, want) {
			t.Errorf("prefixes = %v, want %v", got, want)
		}
	})

	t.Run("cloudflare v4+v6", func(t *testing.T) {
		res, ok := db.LookupASN(13335)
		if !ok {
			t.Fatal("expected found")
		}
		got := prefixStrings(res.Prefixes)
		want := []string{"1.1.1.0/24", "2606:4700::/32"} // v4 sorts before v6
		if !equalStrings(got, want) {
			t.Errorf("prefixes = %v, want %v", got, want)
		}
	})

	t.Run("unknown asn", func(t *testing.T) {
		if _, ok := db.LookupASN(9999); ok {
			t.Error("expected not found")
		}
	})
}

func TestOpenBadFormat(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("garbage"), []byte("ASNLKDB1short")} {
		if _, err := Open(in); err == nil {
			t.Errorf("Open(%q): expected error", in)
		}
	}
}

func TestParseHelpers(t *testing.T) {
	if got := parseASN("AS15169"); got != 15169 {
		t.Errorf("parseASN(AS15169) = %d", got)
	}
	if got := parseASN("15169"); got != 15169 {
		t.Errorf("parseASN(15169) = %d", got)
	}
	if got := parseASN(""); got != 0 {
		t.Errorf("parseASN(empty) = %d, want 0", got)
	}
	if got := parseASN("bogus"); got != 0 {
		t.Errorf("parseASN(bogus) = %d, want 0", got)
	}
	if p, ok := parsePrefix("10.0.0.0/8"); !ok || p.String() != "10.0.0.0/8" {
		t.Errorf("parsePrefix CIDR = %v %v", p, ok)
	}
	if p, ok := parsePrefix("1.2.3.4"); !ok || p.String() != "1.2.3.4/32" {
		t.Errorf("parsePrefix bare = %v %v", p, ok)
	}
	if _, ok := parsePrefix("not-an-ip"); ok {
		t.Error("parsePrefix(not-an-ip) should fail")
	}
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
