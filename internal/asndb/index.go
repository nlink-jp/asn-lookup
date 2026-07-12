package asndb

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"time"
)

// magic identifies the on-disk index format; the trailing digit is the version.
var magic = [8]byte{'A', 'S', 'N', 'L', 'K', 'D', 'B', '1'}

const formatVersion = 1

// v4entry / v6entry are the fixed-width forward-lookup records. Both are sorted
// ascending by start (the masked network address) so containment can be found
// with a single binary search.
type v4entry struct {
	start uint32
	bits  uint8
	rec   uint32
}

type v6entry struct {
	start [16]byte
	bits  uint8
	rec   uint32
}

// Stats summarizes an index for doctor / db_status output.
type Stats struct {
	Generated   time.Time `json:"generated"`
	RecordCount int       `json:"record_count"`
	V4Count     int       `json:"v4_count"`
	V6Count     int       `json:"v6_count"`
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

// Builder accumulates parsed rows and serializes a compact index. Records are
// deduplicated; forward entries are split by address family.
type Builder struct {
	recIndex map[Record]uint32
	records  []Record
	v4       []v4entry
	v6       []v6entry
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{recIndex: make(map[Record]uint32)}
}

// Add ingests one row into the index under construction.
func (b *Builder) Add(row Row) {
	rec := b.intern(row.Record)
	a := row.Network.Addr()
	bits := row.Network.Bits()
	if bits < 0 {
		return
	}
	if a.Is4() {
		b.v4 = append(b.v4, v4entry{start: be32(a.As4()), bits: uint8(bits), rec: rec})
		return
	}
	b.v6 = append(b.v6, v6entry{start: a.As16(), bits: uint8(bits), rec: rec})
}

func (b *Builder) intern(r Record) uint32 {
	if id, ok := b.recIndex[r]; ok {
		return id
	}
	id := uint32(len(b.records))
	b.records = append(b.records, r)
	b.recIndex[r] = id
	return id
}

// Serialize writes the index to w. generatedUnix is the source-data timestamp
// (passed in rather than read from the clock, to keep Build deterministic).
func (b *Builder) Serialize(w io.Writer, generatedUnix int64) error {
	sort.Slice(b.v4, func(i, j int) bool { return b.v4[i].start < b.v4[j].start })
	sort.Slice(b.v6, func(i, j int) bool { return bytes.Compare(b.v6[i].start[:], b.v6[j].start[:]) < 0 })

	// String table with interning.
	var strTab bytes.Buffer
	strOff := make(map[string][2]uint32) // value -> {offset, length}
	ref := func(s string) [2]uint32 {
		if r, ok := strOff[s]; ok {
			return r
		}
		r := [2]uint32{uint32(strTab.Len()), uint32(len(s))}
		strTab.WriteString(s)
		strOff[s] = r
		return r
	}
	// Pre-compute string refs for every record so the table is finalized first.
	type recRefs struct {
		asn                                    uint32
		name, domain, country, cc, cont, ccode [2]uint32
	}
	refs := make([]recRefs, len(b.records))
	for i, r := range b.records {
		refs[i] = recRefs{
			asn:     r.ASN,
			name:    ref(r.ASName),
			domain:  ref(r.ASDomain),
			country: ref(r.Country),
			cc:      ref(r.CountryCode),
			cont:    ref(r.Continent),
			ccode:   ref(r.ContinentCode),
		}
	}

	bw := bufio.NewWriter(w)
	var scratch [21]byte
	putU32 := func(v uint32) { binary.LittleEndian.PutUint32(scratch[:4], v); bw.Write(scratch[:4]) }
	putU64 := func(v uint64) { binary.LittleEndian.PutUint64(scratch[:8], v); bw.Write(scratch[:8]) }

	// Header.
	bw.Write(magic[:])
	putU32(formatVersion)
	putU64(uint64(generatedUnix))
	putU32(uint32(len(b.records)))
	putU32(uint32(len(b.v4)))
	putU32(uint32(len(b.v6)))
	putU32(uint32(strTab.Len()))

	// String table.
	bw.Write(strTab.Bytes())

	// Records: asn + 6 string refs (off,len).
	for _, r := range refs {
		putU32(r.asn)
		for _, sr := range [][2]uint32{r.name, r.domain, r.country, r.cc, r.cont, r.ccode} {
			putU32(sr[0])
			putU32(sr[1])
		}
	}

	// v4 entries: start(4) bits(1) rec(4).
	for _, e := range b.v4 {
		binary.LittleEndian.PutUint32(scratch[0:4], e.start)
		scratch[4] = e.bits
		binary.LittleEndian.PutUint32(scratch[5:9], e.rec)
		bw.Write(scratch[:9])
	}
	// v6 entries: start(16) bits(1) rec(4).
	for _, e := range b.v6 {
		copy(scratch[0:16], e.start[:])
		scratch[16] = e.bits
		binary.LittleEndian.PutUint32(scratch[17:21], e.rec)
		bw.Write(scratch[:21])
	}
	return bw.Flush()
}

// BuildFromCSV streams a decompressed IPinfo Lite CSV from r into a fresh index
// written to w. It returns the resulting Stats and the number of skipped rows.
func BuildFromCSV(r io.Reader, w io.Writer, generatedUnix int64) (Stats, int, error) {
	b := NewBuilder()
	skipped, err := ParseLiteCSV(r, func(row Row) error {
		b.Add(row)
		return nil
	})
	if err != nil {
		return Stats{}, skipped, err
	}
	if err := b.Serialize(w, generatedUnix); err != nil {
		return Stats{}, skipped, err
	}
	st := Stats{
		Generated:   time.Unix(generatedUnix, 0).UTC(),
		RecordCount: len(b.records),
		V4Count:     len(b.v4),
		V6Count:     len(b.v6),
	}
	return st, skipped, nil
}

// ---------------------------------------------------------------------------
// Open / query
// ---------------------------------------------------------------------------

// DB is a queryable index loaded into memory.
type DB struct {
	generated time.Time
	records   []Record
	v4        []v4entry
	v6        []v6entry
}

// ErrBadFormat is returned when the index bytes are not a recognized index.
var ErrBadFormat = errors.New("asndb: unrecognized or corrupt index file")

// Open parses an index previously produced by Serialize/BuildFromCSV.
func Open(data []byte) (*DB, error) {
	const headerLen = 8 + 4 + 8 + 4 + 4 + 4 + 4
	if len(data) < headerLen || !bytes.Equal(data[:8], magic[:]) {
		return nil, ErrBadFormat
	}
	p := 8
	ver := binary.LittleEndian.Uint32(data[p:])
	p += 4
	if ver != formatVersion {
		return nil, fmt.Errorf("%w: version %d (want %d)", ErrBadFormat, ver, formatVersion)
	}
	generated := int64(binary.LittleEndian.Uint64(data[p:]))
	p += 8
	recCount := int(binary.LittleEndian.Uint32(data[p:]))
	p += 4
	v4Count := int(binary.LittleEndian.Uint32(data[p:]))
	p += 4
	v6Count := int(binary.LittleEndian.Uint32(data[p:]))
	p += 4
	strLen := int(binary.LittleEndian.Uint32(data[p:]))
	p += 4

	if p+strLen > len(data) {
		return nil, fmt.Errorf("%w: truncated string table", ErrBadFormat)
	}
	strTab := data[p : p+strLen]
	p += strLen

	str := func(off, length uint32) (string, error) {
		if int(off)+int(length) > len(strTab) {
			return "", fmt.Errorf("%w: string ref out of range", ErrBadFormat)
		}
		return string(strTab[off : off+length]), nil
	}

	// Records.
	const recSize = 4 + 6*8
	if p+recCount*recSize > len(data) {
		return nil, fmt.Errorf("%w: truncated records", ErrBadFormat)
	}
	records := make([]Record, recCount)
	for i := range recCount {
		base := p + i*recSize
		var rec Record
		rec.ASN = binary.LittleEndian.Uint32(data[base:])
		fields := []*string{&rec.ASName, &rec.ASDomain, &rec.Country, &rec.CountryCode, &rec.Continent, &rec.ContinentCode}
		for k, fp := range fields {
			o := binary.LittleEndian.Uint32(data[base+4+k*8:])
			l := binary.LittleEndian.Uint32(data[base+4+k*8+4:])
			s, err := str(o, l)
			if err != nil {
				return nil, err
			}
			*fp = s
		}
		records[i] = rec
	}
	p += recCount * recSize

	// v4.
	const v4Size = 9
	if p+v4Count*v4Size > len(data) {
		return nil, fmt.Errorf("%w: truncated v4 table", ErrBadFormat)
	}
	v4 := make([]v4entry, v4Count)
	for i := range v4Count {
		base := p + i*v4Size
		v4[i] = v4entry{
			start: binary.LittleEndian.Uint32(data[base:]),
			bits:  data[base+4],
			rec:   binary.LittleEndian.Uint32(data[base+5:]),
		}
	}
	p += v4Count * v4Size

	// v6.
	const v6Size = 21
	if p+v6Count*v6Size > len(data) {
		return nil, fmt.Errorf("%w: truncated v6 table", ErrBadFormat)
	}
	v6 := make([]v6entry, v6Count)
	for i := range v6Count {
		base := p + i*v6Size
		var e v6entry
		copy(e.start[:], data[base:base+16])
		e.bits = data[base+16]
		e.rec = binary.LittleEndian.Uint32(data[base+17:])
		v6[i] = e
	}

	return &DB{
		generated: time.Unix(generated, 0).UTC(),
		records:   records,
		v4:        v4,
		v6:        v6,
	}, nil
}

// Generated reports the source-data timestamp recorded at build time.
func (db *DB) Generated() time.Time { return db.generated }

// Stats returns index counts and the generated timestamp.
func (db *DB) Stats() Stats {
	return Stats{
		Generated:   db.generated,
		RecordCount: len(db.records),
		V4Count:     len(db.v4),
		V6Count:     len(db.v6),
	}
}

// LookupIP returns the AS/geo record for the network containing ip.
func (db *DB) LookupIP(ip netip.Addr) (IPResult, bool) {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return IPResult{}, false
	}
	if ip.Is4() {
		key := be32(ip.As4())
		i := sort.Search(len(db.v4), func(i int) bool { return db.v4[i].start > key })
		if i == 0 {
			return IPResult{}, false
		}
		e := db.v4[i-1]
		pfx := netip.PrefixFrom(addr4(e.start), int(e.bits))
		if !pfx.Contains(ip) {
			return IPResult{}, false
		}
		return db.result(ip, pfx, e.rec), true
	}
	key := ip.As16()
	i := sort.Search(len(db.v6), func(i int) bool { return bytes.Compare(db.v6[i].start[:], key[:]) > 0 })
	if i == 0 {
		return IPResult{}, false
	}
	e := db.v6[i-1]
	pfx := netip.PrefixFrom(netip.AddrFrom16(e.start), int(e.bits))
	if !pfx.Contains(ip) {
		return IPResult{}, false
	}
	return db.result(ip, pfx, e.rec), true
}

func (db *DB) result(ip netip.Addr, pfx netip.Prefix, rec uint32) IPResult {
	r := db.records[rec]
	return IPResult{
		IP:            ip,
		Network:       pfx,
		ASN:           r.ASN,
		ASName:        r.ASName,
		ASDomain:      r.ASDomain,
		Country:       r.Country,
		CountryCode:   r.CountryCode,
		Continent:     r.Continent,
		ContinentCode: r.ContinentCode,
	}
}

// LookupASN returns every prefix mapped to asn, plus the AS name/domain. It is
// a linear scan of the forward entries: the reverse view is derived from the
// same data, so it can never drift from LookupIP. found is false when asn has
// no networks in the database.
func (db *DB) LookupASN(asn uint32) (ASNResult, bool) {
	res := ASNResult{ASN: asn}
	for _, e := range db.v4 {
		r := db.records[e.rec]
		if r.ASN != asn {
			continue
		}
		if res.ASName == "" {
			res.ASName, res.ASDomain = r.ASName, r.ASDomain
		}
		res.Prefixes = append(res.Prefixes, netip.PrefixFrom(addr4(e.start), int(e.bits)))
	}
	for _, e := range db.v6 {
		r := db.records[e.rec]
		if r.ASN != asn {
			continue
		}
		if res.ASName == "" {
			res.ASName, res.ASDomain = r.ASName, r.ASDomain
		}
		res.Prefixes = append(res.Prefixes, netip.PrefixFrom(netip.AddrFrom16(e.start), int(e.bits)))
	}
	if len(res.Prefixes) == 0 {
		return ASNResult{}, false
	}
	sort.Slice(res.Prefixes, func(i, j int) bool {
		if c := res.Prefixes[i].Addr().Compare(res.Prefixes[j].Addr()); c != 0 {
			return c < 0
		}
		return res.Prefixes[i].Bits() < res.Prefixes[j].Bits()
	})
	return res, true
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func be32(b [4]byte) uint32 { return binary.BigEndian.Uint32(b[:]) }

func addr4(u uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], u)
	return netip.AddrFrom4(b)
}
