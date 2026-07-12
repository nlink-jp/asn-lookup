// Package app implements the asn-lookup command-line interface: subcommand
// dispatch plus the ip / asn / update / doctor / mcp commands. Core logic lives
// in the asndb, config, engine, ipinfo, and mcp packages; this package is the
// thin I/O shell around them.
package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nlink-jp/asn-lookup/internal/asndb"
	"github.com/nlink-jp/asn-lookup/internal/config"
	"github.com/nlink-jp/asn-lookup/internal/engine"
	"github.com/nlink-jp/asn-lookup/internal/ipinfo"
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "ip":
		return cmdIP(rest)
	case "asn":
		return cmdASN(rest)
	case "update":
		return cmdUpdate(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "mcp":
		return cmdMCP(rest, version)
	case "version", "--version", "-v":
		fmt.Println("asn-lookup " + version)
		fmt.Println("Data: IPinfo Lite (https://ipinfo.io/lite), CC BY-SA 4.0.")
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `asn-lookup — local IP↔AS lookups from the IPinfo Lite database

Usage:
  asn-lookup <command> [flags] [args]

Commands:
  ip <IP>...      Look up AS + country/continent for each IP (stdin if no args)
  asn <ASN>...    List the IP prefixes announced by each ASN (stdin if no args)
  update          Download the IPinfo Lite database and rebuild the local index
  doctor          Check database presence, freshness, and configuration
  mcp             Run as a local MCP server (stdio)
  version         Print the version

Common flags:
  -c, --config <path>   Config file (default ~/.config/asn-lookup/config.toml)
  --db <path>           Index file (default ~/.local/share/asn-lookup/asndb.bin)
  -j, --json            JSON Lines output (ip / asn)

Configuration:
  Token via IPINFO_TOKEN env var or [ipinfo] token in the config file.

Data: IPinfo Lite (https://ipinfo.io/lite), CC BY-SA 4.0. Attribution required.
`)
}

// commonFlags are the config-resolution flags shared by every command.
type commonFlags struct {
	config string
	db     string
	token  string
	url    string
}

// register binds the common flags onto fs. When withUpdate is true it also
// registers --token / --url (only meaningful for commands that download).
func (c *commonFlags) register(fs *flag.FlagSet, withUpdate bool) {
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.StringVar(&c.config, "c", "", "config file path (shorthand)")
	fs.StringVar(&c.db, "db", "", "index file path override")
	if withUpdate {
		fs.StringVar(&c.token, "token", "", "ipinfo token override")
		fs.StringVar(&c.url, "url", "", "Lite CSV URL override")
	}
}

// parseInterspersed parses fs while tolerating flags that appear after
// positional arguments (Go's flag package otherwise stops at the first
// non-flag). It returns the collected positional arguments. Inputs never begin
// with '-', so there is no ambiguity.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}

func (c *commonFlags) buildEngine() (*engine.Engine, error) {
	cfg, err := config.Load(c.config, c.db, c.token, c.url)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg, ipinfo.NewHTTPFetcher()), nil
}

// readInputs returns args verbatim, or whitespace-separated tokens read from
// stdin when args is empty. Blank lines and '#' comment lines are skipped.
func readInputs(args []string, stdin io.Reader) []string {
	if len(args) > 0 {
		return args
	}
	var out []string
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}

// loadDBOrHint loads the index, printing an actionable hint on ErrNoDB.
func loadDBOrHint(e *engine.Engine, errw io.Writer) (*asndb.DB, int) {
	db, err := e.LoadDB()
	if err != nil {
		if errors.Is(err, engine.ErrNoDB) {
			fmt.Fprintf(errw, "%v\nrun 'asn-lookup update' to download the IPinfo Lite database.\n", err)
			return nil, 1
		}
		fmt.Fprintf(errw, "error: %v\n", err)
		return nil, 1
	}
	return db, 0
}

// warnIfStale prints a freshness warning to errw; it never updates on its own.
func warnIfStale(e *engine.Engine, db *asndb.DB, errw io.Writer) {
	if stale, age := e.IsStale(db.Generated()); stale {
		fmt.Fprintf(errw, "warning: database is %d days old (generated %s); run 'asn-lookup update'\n",
			int(age.Hours()/24), db.Generated().Format("2006-01-02"))
	}
}
