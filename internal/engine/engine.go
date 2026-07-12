// Package engine ties configuration, downloading, and the on-disk index
// together. Both the CLI and the MCP server drive the same Engine so their
// behaviour cannot diverge.
package engine

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/config"
	"github.com/nlink-jp/asn-lookup/internal/ipinfo"
)

// StaleAfter is the age past which a database is considered out of date.
const StaleAfter = 30 * 24 * time.Hour

// Errors surfaced to callers for friendly handling.
var (
	// ErrNoDB means no index exists yet; the caller should suggest `update`.
	ErrNoDB = errors.New("no local database")
	// ErrNoToken means no ipinfo token is configured.
	ErrNoToken = errors.New("no ipinfo token configured (set IPINFO_TOKEN or [ipinfo] token in config)")
)

// Engine performs load and update operations against the configured index.
type Engine struct {
	Cfg     *config.Config
	Fetcher ipinfo.Fetcher
	Now     func() time.Time // injectable clock; defaults to time.Now
}

// New returns an Engine with the given config and fetcher.
func New(cfg *config.Config, fetcher ipinfo.Fetcher) *Engine {
	return &Engine{Cfg: cfg, Fetcher: fetcher, Now: time.Now}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// LoadDB reads and opens the local index. It returns ErrNoDB (wrapped) when the
// file does not exist.
func (e *Engine) LoadDB() (*asndb.DB, error) {
	data, err := os.ReadFile(e.Cfg.DBPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoDB, e.Cfg.DBPath)
		}
		return nil, err
	}
	return asndb.Open(data)
}

// Update downloads the IPinfo Lite database, rebuilds the index, and atomically
// replaces the local file. It returns the new Stats and the number of skipped
// (unparseable) rows.
func (e *Engine) Update(ctx context.Context) (asndb.Stats, int, error) {
	if e.Cfg.Token == "" {
		return asndb.Stats{}, 0, ErrNoToken
	}

	body, err := e.Fetcher.Fetch(ctx, e.Cfg.LiteURL, e.Cfg.Token)
	if err != nil {
		return asndb.Stats{}, 0, err
	}
	defer body.Close()

	var src io.Reader = body
	if strings.HasSuffix(e.Cfg.LiteURL, ".gz") {
		gz, gzErr := gzip.NewReader(body)
		if gzErr != nil {
			return asndb.Stats{}, 0, fmt.Errorf("gzip: %w", gzErr)
		}
		defer gz.Close()
		src = gz
	}

	dir := filepath.Dir(e.Cfg.DBPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return asndb.Stats{}, 0, fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "asndb-*.tmp")
	if err != nil {
		return asndb.Stats{}, 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	stats, skipped, err := asndb.BuildFromCSV(src, tmp, e.now().Unix())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return asndb.Stats{}, skipped, err
	}
	if err := os.Rename(tmpName, e.Cfg.DBPath); err != nil {
		return asndb.Stats{}, skipped, fmt.Errorf("install index: %w", err)
	}
	return stats, skipped, nil
}

// IsStale reports whether a database generated at gen is older than StaleAfter
// relative to the engine's clock, and the age.
func (e *Engine) IsStale(gen time.Time) (bool, time.Duration) {
	age := e.now().Sub(gen)
	return age > StaleAfter, age
}
