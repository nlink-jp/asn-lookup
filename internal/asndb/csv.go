package asndb

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// Row is one parsed line of the IPinfo Lite CSV.
type Row struct {
	Network netip.Prefix
	Record
}

// ParseLiteCSV reads a decompressed IPinfo Lite CSV from r and invokes fn once
// per data row. Rows with an unparseable network are skipped and counted; the
// skip count is returned. fn may return an error to abort early.
func ParseLiteCSV(r io.Reader, fn func(Row) error) (skipped int, err error) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we index by name

	header, err := cr.Read()
	if err != nil {
		return 0, fmt.Errorf("read header: %w", err)
	}
	idx, err := columnIndex(header)
	if err != nil {
		return 0, err
	}

	for {
		rec, rerr := cr.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return skipped, fmt.Errorf("read row: %w", rerr)
		}

		network, ok := parsePrefix(field(rec, idx["network"]))
		if !ok {
			skipped++
			continue
		}
		row := Row{
			Network: network,
			Record: Record{
				ASN:           parseASN(field(rec, idx["asn"])),
				ASName:        field(rec, idx["as_name"]),
				ASDomain:      field(rec, idx["as_domain"]),
				Country:       field(rec, idx["country"]),
				CountryCode:   field(rec, idx["country_code"]),
				Continent:     field(rec, idx["continent"]),
				ContinentCode: field(rec, idx["continent_code"]),
			},
		}
		if err := fn(row); err != nil {
			return skipped, err
		}
	}
	return skipped, nil
}

// columnIndex maps each required Lite column name to its position in header.
func columnIndex(header []string) (map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.TrimSpace(name)] = i
	}
	// "network" is mandatory; the rest we tolerate as empty if absent.
	if _, ok := idx["network"]; !ok {
		return nil, fmt.Errorf("CSV missing required column %q (got %v)", "network", header)
	}
	return idx, nil
}

// field returns the value at position i, or "" when i is out of range/absent.
func field(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// parsePrefix accepts either CIDR ("1.2.3.0/24") or a bare address, returning a
// masked prefix. A bare address becomes a host prefix (/32 or /128).
func parsePrefix(s string) (netip.Prefix, bool) {
	if s == "" {
		return netip.Prefix{}, false
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

// ParseASN parses user input ("AS15169" or "15169") into a numeric ASN. ok is
// false for empty, unparseable, or zero input (ASN 0 is not a valid AS).
func ParseASN(s string) (asn uint32, ok bool) {
	n := parseASN(s)
	return n, n != 0
}

// parseASN parses "AS15169" or "15169" into a numeric ASN. Unparseable or empty
// input yields 0 ("no AS mapping").
func parseASN(s string) uint32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "AS")
	s = strings.TrimPrefix(s, "as")
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}
