// Package config resolves asn-lookup settings from a sectioned TOML file plus
// environment overrides. It parses only the small TOML subset the tool needs,
// keeping the binary free of external dependencies.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultLiteURL is the IPinfo Lite CSV download endpoint (CC BY-SA 4.0).
const DefaultLiteURL = "https://ipinfo.io/data/ipinfo_lite.csv.gz"

// Config holds resolved runtime settings.
type Config struct {
	Token     string // ipinfo download token (secret; never logged verbatim)
	LiteURL   string // Lite CSV download URL
	DBPath    string // path to the local compact index
	Workspace string // default MCP output directory for file-mediated results
}

// Load resolves configuration. If configPath is empty the default location
// (~/.config/asn-lookup/config.toml) is used when present. Environment
// variables override file values, and any explicit non-empty override* argument
// wins over both.
func Load(configPath, dbOverride, tokenOverride, urlOverride string) (*Config, error) {
	cfg := &Config{
		LiteURL:   DefaultLiteURL,
		DBPath:    DefaultDBPath(),
		Workspace: DefaultWorkspaceDir(),
	}

	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	if configPath != "" {
		if f, err := os.Open(configPath); err == nil {
			defer f.Close()
			sections, perr := parseTOML(f)
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			applySections(cfg, sections)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Environment overrides (tool-specific first, then the ipinfo-standard name).
	if v := firstEnv("ASN_LOOKUP_TOKEN", "IPINFO_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := firstEnv("ASN_LOOKUP_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := firstEnv("ASN_LOOKUP_LITE_URL"); v != "" {
		cfg.LiteURL = v
	}
	if v := firstEnv("ASN_LOOKUP_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}

	// Explicit flag overrides win.
	if tokenOverride != "" {
		cfg.Token = tokenOverride
	}
	if dbOverride != "" {
		cfg.DBPath = dbOverride
	}
	if urlOverride != "" {
		cfg.LiteURL = urlOverride
	}

	return cfg, nil
}

func applySections(cfg *Config, sections map[string]map[string]string) {
	if ip := sections["ipinfo"]; ip != nil {
		if v := ip["token"]; v != "" {
			cfg.Token = v
		}
		if v := ip["lite_url"]; v != "" {
			cfg.LiteURL = v
		}
	}
	if db := sections["db"]; db != nil {
		if v := db["path"]; v != "" {
			cfg.DBPath = expandHome(v)
		}
	}
	if mcp := sections["mcp"]; mcp != nil {
		if v := mcp["workspace"]; v != "" {
			cfg.Workspace = expandHome(v)
		}
	}
}

// DefaultConfigPath returns the default config file location, honoring
// XDG_CONFIG_HOME.
func DefaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "asn-lookup", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "asn-lookup", "config.toml")
}

// DefaultDBPath returns the default index location, honoring XDG_DATA_HOME.
func DefaultDBPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "asn-lookup", "asndb.bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "asndb.bin"
	}
	return filepath.Join(home, ".local", "share", "asn-lookup", "asndb.bin")
}

// DefaultWorkspaceDir returns the default MCP output directory, honoring
// XDG_STATE_HOME (file-mediated results are reproducible, transient state).
func DefaultWorkspaceDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "asn-lookup", "workspace")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "asn-lookup", "workspace")
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// parseTOML parses the minimal subset asn-lookup needs: [section] headers and
// key = value lines, where value is an optionally quoted string. Comments start
// with '#'. It intentionally does not support arrays, nested tables, or typed
// values.
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := "" // top-level keys land in the "" section
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			current = strings.TrimSpace(raw[1:end])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		val := parseValue(strings.TrimSpace(raw[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue strips surrounding quotes, or trims a trailing inline comment from
// a bare value.
func parseValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v
}
