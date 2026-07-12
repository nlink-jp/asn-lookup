package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"text/tabwriter"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
)

// ipJSON is the JSONL shape for `ip` results (one object per line).
type ipJSON struct {
	IP            string `json:"ip"`
	Found         bool   `json:"found"`
	Error         string `json:"error,omitempty"`
	Network       string `json:"network,omitempty"`
	ASN           uint32 `json:"asn,omitempty"`
	ASName        string `json:"as_name,omitempty"`
	ASDomain      string `json:"as_domain,omitempty"`
	Country       string `json:"country,omitempty"`
	CountryCode   string `json:"country_code,omitempty"`
	Continent     string `json:"continent,omitempty"`
	ContinentCode string `json:"continent_code,omitempty"`
}

func ipResultJSON(input string, r asndb.IPResult, found bool) ipJSON {
	if !found {
		return ipJSON{IP: input, Found: false}
	}
	return ipJSON{
		IP:            r.IP.String(),
		Found:         true,
		Network:       r.Network.String(),
		ASN:           r.ASN,
		ASName:        r.ASName,
		ASDomain:      r.ASDomain,
		Country:       r.Country,
		CountryCode:   r.CountryCode,
		Continent:     r.Continent,
		ContinentCode: r.ContinentCode,
	}
}

// asnJSON is the JSONL shape for `asn` results.
type asnJSON struct {
	ASN         uint32   `json:"asn"`
	Found       bool     `json:"found"`
	ASName      string   `json:"as_name,omitempty"`
	ASDomain    string   `json:"as_domain,omitempty"`
	PrefixCount int      `json:"prefix_count"`
	V4Count     int      `json:"v4_count"`
	V6Count     int      `json:"v6_count"`
	Truncated   bool     `json:"truncated,omitempty"`
	Prefixes    []string `json:"prefixes,omitempty"`
}

// asnResultJSON builds the JSON view. limit<=0 inlines all prefixes; countOnly
// omits the prefix list entirely.
func asnResultJSON(r asndb.ASNResult, found bool, limit int, countOnly bool) asnJSON {
	if !found {
		return asnJSON{ASN: r.ASN, Found: false}
	}
	v4, v6 := r.FamilyCounts()
	j := asnJSON{
		ASN: r.ASN, Found: true,
		ASName: r.ASName, ASDomain: r.ASDomain,
		PrefixCount: len(r.Prefixes), V4Count: v4, V6Count: v6,
	}
	if countOnly {
		return j
	}
	all := prefixStrings(r.Prefixes)
	if limit > 0 && limit < len(all) {
		j.Prefixes = all[:limit]
		j.Truncated = true
	} else {
		j.Prefixes = all
	}
	return j
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// jsonLine writes v as a single JSON line.
func jsonLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

// ipTable renders `ip` results as an aligned table.
type ipTable struct{ tw *tabwriter.Writer }

func newIPTable(w io.Writer) *ipTable {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "IP\tASN\tAS NAME\tCOUNTRY\tNETWORK")
	return &ipTable{tw: tw}
}

func (t *ipTable) row(input string, r asndb.IPResult, found bool, invalid bool) {
	switch {
	case invalid:
		fmt.Fprintf(t.tw, "%s\t-\t(invalid)\t-\t-\n", input)
	case !found:
		fmt.Fprintf(t.tw, "%s\t-\t(not found)\t-\t-\n", input)
	default:
		asn := "-"
		if r.ASN != 0 {
			asn = "AS" + strconv.FormatUint(uint64(r.ASN), 10)
		}
		name := r.ASName
		if name == "" {
			name = "-"
		}
		country := r.CountryCode
		if country == "" {
			country = "-"
		}
		fmt.Fprintf(t.tw, "%s\t%s\t%s\t%s\t%s\n", r.IP, asn, name, country, r.Network)
	}
}

func (t *ipTable) flush() { t.tw.Flush() }

// asnBlock renders a single `asn` result: a header line, then its prefixes.
// limit<=0 prints all prefixes; countOnly prints only the header.
func asnBlock(w io.Writer, r asndb.ASNResult, found bool, limit int, countOnly bool) {
	if !found {
		fmt.Fprintf(w, "AS%d  (not found)\n", r.ASN)
		return
	}
	name := r.ASName
	if name == "" {
		name = "-"
	}
	v4, v6 := r.FamilyCounts()
	fmt.Fprintf(w, "AS%d  %s  %s  (%d prefixes; v4 %d, v6 %d)\n", r.ASN, name, r.ASDomain, len(r.Prefixes), v4, v6)
	if countOnly {
		return
	}
	n := len(r.Prefixes)
	show := n
	if limit > 0 && limit < n {
		show = limit
	}
	for _, p := range r.Prefixes[:show] {
		fmt.Fprintf(w, "  %s\n", p)
	}
	if show < n {
		fmt.Fprintf(w, "  … (showing %d of %d)\n", show, n)
	}
}
