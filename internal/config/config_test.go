package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTOML(t *testing.T) {
	const in = `
# comment line
lite_url_top = "ignored-at-top"

[ipinfo]
token = "secret-123"     # inline comment
lite_url = 'https://example.test/lite.csv.gz'

[db]
path = ~/data/asndb.bin
`
	sections, err := parseTOML(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	if got := sections["ipinfo"]["token"]; got != "secret-123" {
		t.Errorf("token = %q", got)
	}
	if got := sections["ipinfo"]["lite_url"]; got != "https://example.test/lite.csv.gz" {
		t.Errorf("lite_url = %q", got)
	}
	if got := sections["db"]["path"]; got != "~/data/asndb.bin" {
		t.Errorf("db.path = %q", got)
	}
}

func TestParseValue(t *testing.T) {
	cases := map[string]string{
		`"quoted"`:        "quoted",
		`'single'`:        "single",
		`bare`:            "bare",
		`bare # trailing`: "bare",
		`"with # hash"`:   "with # hash",
	}
	for in, want := range cases {
		if got := parseValue(in); got != want {
			t.Errorf("parseValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTOMLErrors(t *testing.T) {
	for _, in := range []string{"[unterminated\n", "novalue\n"} {
		if _, err := parseTOML(strings.NewReader(in)); err == nil {
			t.Errorf("parseTOML(%q): expected error", in)
		}
	}
}

func TestLoadFileAndEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[ipinfo]\ntoken = \"from-file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// File value used when no env is set.
	t.Setenv("ASN_LOOKUP_TOKEN", "")
	t.Setenv("IPINFO_TOKEN", "")
	cfg, err := Load(cfgPath, "", "", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "from-file" {
		t.Errorf("token = %q, want from-file", cfg.Token)
	}
	if cfg.LiteURL != DefaultLiteURL {
		t.Errorf("LiteURL = %q, want default", cfg.LiteURL)
	}

	// Env overrides file.
	t.Setenv("IPINFO_TOKEN", "from-env")
	cfg, _ = Load(cfgPath, "", "", "")
	if cfg.Token != "from-env" {
		t.Errorf("token = %q, want from-env", cfg.Token)
	}

	// Flag override wins over env.
	cfg, _ = Load(cfgPath, "/tmp/custom.bin", "from-flag", "https://flag.test/x.gz")
	if cfg.Token != "from-flag" || cfg.DBPath != "/tmp/custom.bin" || cfg.LiteURL != "https://flag.test/x.gz" {
		t.Errorf("flag overrides not applied: %+v", cfg)
	}
}

func TestDefaultPathsHonorXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	if got := DefaultConfigPath(); got != "/xdg/config/asn-lookup/config.toml" {
		t.Errorf("DefaultConfigPath = %q", got)
	}
	if got := DefaultDBPath(); got != "/xdg/data/asn-lookup/asndb.bin" {
		t.Errorf("DefaultDBPath = %q", got)
	}
}
