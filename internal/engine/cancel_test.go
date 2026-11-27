package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// cancelAtEOF serves the whole download and cancels the update as it ends.
type cancelAtEOF struct {
	data   []byte
	cancel context.CancelFunc
}

func (c cancelAtEOF) Fetch(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(&eofCanceller{r: bytes.NewReader(c.data), cancel: c.cancel}), nil
}

type eofCanceller struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (e *eofCanceller) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		e.cancel()
	}
	return n, err
}

// An update cancelled once its download is read leaves the database as it
// was and no temp file beside it.
func TestAnUpdateCancelledAtTheEndOfItsDownloadInstallsNothing(t *testing.T) {
	e := newEngine(t, fakeFetcher{data: gzipBytes(t, sampleCSV)}, "tok")
	if _, _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(e.Cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Fetcher = cancelAtEOF{data: gzipBytes(t, sampleCSV), cancel: cancel}
	if _, _, err := e.Update(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Update = %v, want context.Canceled", err)
	}
	after, _ := os.ReadFile(e.Cfg.DBPath)
	if !bytes.Equal(before, after) {
		t.Error("the database was replaced although the update was cancelled")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(e.Cfg.DBPath), "asndb-*.tmp"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}
