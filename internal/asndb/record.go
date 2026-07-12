// Package asndb ingests the IPinfo Lite database and answers IP-to-AS and
// ASN-to-prefix lookups from a compact local index.
//
// Data source: IPinfo Lite (https://ipinfo.io/lite), CC BY-SA 4.0.
// Attribution to IPinfo is required by the license; see the README.
package asndb

import "net/netip"

// Record holds the AS and geo attributes shared by one or more IP networks.
// It is deduplicated in the on-disk index: many networks reference the same
// Record.
type Record struct {
	ASN           uint32 // numeric ASN; 0 means "no AS mapping"
	ASName        string
	ASDomain      string
	Country       string
	CountryCode   string
	Continent     string
	ContinentCode string
}

// IPResult is the forward-lookup result for a single IP address.
type IPResult struct {
	IP            netip.Addr   `json:"ip"`
	Network       netip.Prefix `json:"network"`
	ASN           uint32       `json:"asn"`
	ASName        string       `json:"as_name"`
	ASDomain      string       `json:"as_domain"`
	Country       string       `json:"country"`
	CountryCode   string       `json:"country_code"`
	Continent     string       `json:"continent"`
	ContinentCode string       `json:"continent_code"`
}

// ASNResult is the reverse-lookup result for a single ASN.
type ASNResult struct {
	ASN      uint32         `json:"asn"`
	ASName   string         `json:"as_name"`
	ASDomain string         `json:"as_domain"`
	Prefixes []netip.Prefix `json:"prefixes"`
}

// FamilyCounts returns how many of the prefixes are IPv4 and IPv6.
func (r ASNResult) FamilyCounts() (v4, v6 int) {
	for _, p := range r.Prefixes {
		if p.Addr().Is4() {
			v4++
		} else {
			v6++
		}
	}
	return v4, v6
}
