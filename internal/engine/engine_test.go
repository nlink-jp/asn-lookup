package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/asn-lookup/internal/config"
)

const sampleCSV = `network,country,country_code,continent,continent_code,asn,as_name,as_domain
8.8.8.0/24,United States,US,North America,NA,AS15169,Google LLC,google.com
`

type fakeFetcher struct {
	data []byte
	err  error
}

func (f fakeFetcher) Fetch(_ context.Context, _, _ string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newEngine(t *testing.T, f fakeFetcher, token string) *Engine {
	t.Helper()
	cfg := &config.Config{
		Token:   token,
		LiteURL: "https://example.test/ipinfo_lite.csv.gz",
		DBPath:  filepath.Join(t.TempDir(), "asndb.bin"),
	}
	e := New(cfg, f)
	e.Now = func() time.Time { return time.Unix(1720000000, 0) }
	return e
}

func TestUpdateThenLookup(t *testing.T) {
	e := newEngine(t, fakeFetcher{data: gzipBytes(t, sampleCSV)}, "tok")

	stats, skipped, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if skipped != 0 || stats.V4Count != 1 {
		t.Fatalf("stats = %+v skipped=%d", stats, skipped)
	}

	db, err := e.LoadDB()
	if err != nil {
		t.Fatalf("LoadDB: %v", err)
	}
	res, ok := db.LookupIP(netip.MustParseAddr("8.8.8.8"))
	if !ok || res.ASN != 15169 {
		t.Fatalf("lookup = %+v ok=%v", res, ok)
	}
}

func TestUpdateNoToken(t *testing.T) {
	e := newEngine(t, fakeFetcher{data: gzipBytes(t, sampleCSV)}, "")
	if _, _, err := e.Update(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestLoadDBMissing(t *testing.T) {
	e := newEngine(t, fakeFetcher{}, "tok")
	if _, err := e.LoadDB(); !errors.Is(err, ErrNoDB) {
		t.Fatalf("err = %v, want ErrNoDB", err)
	}
}

func TestIsStale(t *testing.T) {
	e := newEngine(t, fakeFetcher{}, "tok") // clock at 1720000000
	fresh := time.Unix(1720000000, 0).Add(-1 * time.Hour)
	if stale, _ := e.IsStale(fresh); stale {
		t.Error("1h old should not be stale")
	}
	old := time.Unix(1720000000, 0).Add(-40 * 24 * time.Hour)
	if stale, age := e.IsStale(old); !stale || age < StaleAfter {
		t.Errorf("40d old should be stale (age=%v)", age)
	}
}
