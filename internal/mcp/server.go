// Package mcp serves asn-lookup's database as Model Context Protocol tools over
// stdio. The protocol — both eras, framing, versions, cancellation — is
// nlink-jp/mcp's (organization ADR-030, ADR-0001); the tool conventions —
// argument decoding, structured errors, results, serving — are
// nlink-jp/mcptool's (organization ADR-031, ADR-0002). This package holds the
// tools, their schemas, their error codes and the database cache.
package mcp

import (
	"os"
	"time"

	"github.com/nlink-jp/mcptool"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/engine"
)

// server holds the engine and a modtime-keyed database cache so repeated tool
// calls do not reparse the index, while still picking up update_db changes.
// The cache has no lock: the server runs one call at a time (Workers 1, the
// mcptool default).
type server struct {
	e     *engine.Engine
	db    *asndb.DB
	dbMod time.Time
}

// New returns the MCP server for e, its tools registered in their listed
// order. Serve it with mcptool/serve's Run.
func New(e *engine.Engine, version string) (*mcptool.Server, error) {
	s := &server{e: e}
	ms, err := mcptool.New(mcptool.Info{Name: "asn-lookup", Version: version, Instructions: Instructions}, mcptool.Options{})
	if err != nil {
		return nil, err
	}
	if err := s.register(ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// database returns the cached index, reloading it when the file changed on disk.
func (s *server) database() (*asndb.DB, error) {
	fi, err := os.Stat(s.e.Cfg.DBPath)
	if err != nil {
		// Fall through to LoadDB so the caller gets the wrapped ErrNoDB message.
		return s.e.LoadDB()
	}
	if s.db != nil && fi.ModTime().Equal(s.dbMod) {
		return s.db, nil
	}
	db, err := s.e.LoadDB()
	if err != nil {
		return nil, err
	}
	s.db, s.dbMod = db, fi.ModTime()
	return db, nil
}
