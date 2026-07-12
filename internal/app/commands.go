package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"time"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/config"
	"github.com/nlink-jp/asn-lookup/internal/engine"
	"github.com/nlink-jp/asn-lookup/internal/ipinfo"
	"github.com/nlink-jp/asn-lookup/internal/mcp"
)

// ---- ip -------------------------------------------------------------------

func cmdIP(args []string) int {
	fs := flag.NewFlagSet("ip", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "JSON Lines output")
	fs.BoolVar(&jsonOut, "j", false, "JSON Lines output (shorthand)")
	inputs, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return runIP(os.Stdout, os.Stderr, os.Stdin, e, jsonOut, inputs)
}

func runIP(out, errw io.Writer, stdin io.Reader, e *engine.Engine, jsonOut bool, args []string) int {
	inputs := readInputs(args, stdin)
	if len(inputs) == 0 {
		fmt.Fprintln(errw, "no IP addresses given (pass as arguments or on stdin)")
		return 2
	}
	db, code := loadDBOrHint(e, errw)
	if code != 0 {
		return code
	}
	warnIfStale(e, db, errw)

	var tbl *ipTable
	if !jsonOut {
		tbl = newIPTable(out)
	}
	for _, in := range inputs {
		addr, err := netip.ParseAddr(in)
		if err != nil {
			if jsonOut {
				_ = jsonLine(out, ipJSON{IP: in, Found: false, Error: "invalid address"})
			} else {
				tbl.row(in, asndb.IPResult{}, false, true)
			}
			continue
		}
		res, ok := db.LookupIP(addr)
		if jsonOut {
			_ = jsonLine(out, ipResultJSON(in, res, ok))
		} else {
			tbl.row(in, res, ok, false)
		}
	}
	if tbl != nil {
		tbl.flush()
	}
	return 0
}

// ---- asn ------------------------------------------------------------------

func cmdASN(args []string) int {
	fs := flag.NewFlagSet("asn", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	var jsonOut, countOnly bool
	var limit int
	fs.BoolVar(&jsonOut, "json", false, "JSON Lines output")
	fs.BoolVar(&jsonOut, "j", false, "JSON Lines output (shorthand)")
	fs.BoolVar(&countOnly, "count", false, "print only the prefix counts")
	fs.IntVar(&limit, "limit", 0, "max prefixes to print per ASN (0 = all)")
	fs.IntVar(&limit, "n", 0, "max prefixes to print per ASN (shorthand)")
	inputs, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return runASN(os.Stdout, os.Stderr, os.Stdin, e, jsonOut, countOnly, limit, inputs)
}

func runASN(out, errw io.Writer, stdin io.Reader, e *engine.Engine, jsonOut, countOnly bool, limit int, args []string) int {
	inputs := readInputs(args, stdin)
	if len(inputs) == 0 {
		fmt.Fprintln(errw, "no ASNs given (pass as arguments or on stdin)")
		return 2
	}
	db, code := loadDBOrHint(e, errw)
	if code != 0 {
		return code
	}
	warnIfStale(e, db, errw)

	for _, in := range inputs {
		asn, ok := asndb.ParseASN(in)
		if !ok {
			fmt.Fprintf(errw, "skipping invalid ASN %q\n", in)
			continue
		}
		res, found := db.LookupASN(asn)
		res.ASN = asn
		if jsonOut {
			_ = jsonLine(out, asnResultJSON(res, found, limit, countOnly))
		} else {
			asnBlock(out, res, found, limit, countOnly)
		}
	}
	return 0
}

// ---- update ---------------------------------------------------------------

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return runUpdate(os.Stdout, os.Stderr, e)
}

func runUpdate(out, errw io.Writer, e *engine.Engine) int {
	fmt.Fprintf(errw, "downloading %s …\n", ipinfo.Redact(e.Cfg.LiteURL))
	stats, skipped, err := e.Update(context.Background())
	if err != nil {
		if errors.Is(err, engine.ErrNoToken) {
			fmt.Fprintf(errw, "%v\n", err)
			return 1
		}
		fmt.Fprintf(errw, "update failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "updated %s\n", e.Cfg.DBPath)
	fmt.Fprintf(out, "  generated: %s\n  records: %d  v4: %d  v6: %d  skipped: %d\n",
		stats.Generated.Format(time.RFC3339), stats.RecordCount, stats.V4Count, stats.V6Count, skipped)
	return 0
}

// ---- doctor ---------------------------------------------------------------

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, false)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	cfgPath := c.config
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	return runDoctor(os.Stdout, e, cfgPath)
}

func runDoctor(out io.Writer, e *engine.Engine, cfgPath string) int {
	ok := true
	fmt.Fprintf(out, "config file: %s (%s)\n", cfgPath, existsLabel(cfgPath))
	if e.Cfg.Token != "" {
		fmt.Fprintln(out, "token:       configured")
	} else {
		fmt.Fprintln(out, "token:       MISSING (needed for 'update'; set IPINFO_TOKEN)")
	}
	fmt.Fprintf(out, "database:    %s\n", e.Cfg.DBPath)

	db, err := e.LoadDB()
	if err != nil {
		ok = false
		fmt.Fprintf(out, "  status:    ERROR — %v\n", err)
	} else {
		st := db.Stats()
		fmt.Fprintf(out, "  generated: %s\n", st.Generated.Format(time.RFC3339))
		fmt.Fprintf(out, "  records:   %d  (v4: %d, v6: %d)\n", st.RecordCount, st.V4Count, st.V6Count)
		if stale, age := e.IsStale(st.Generated); stale {
			fmt.Fprintf(out, "  status:    STALE — %d days old; run 'asn-lookup update'\n", int(age.Hours()/24))
		} else {
			fmt.Fprintln(out, "  status:    OK")
		}
	}
	if !ok {
		return 1
	}
	return 0
}

func existsLabel(path string) string {
	if path == "" {
		return "unset"
	}
	if _, err := os.Stat(path); err == nil {
		return "found"
	}
	return "not found"
}

// ---- mcp ------------------------------------------------------------------

func cmdMCP(args []string, version string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	e, err := c.buildEngine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := mcp.Serve(context.Background(), e, version, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}
