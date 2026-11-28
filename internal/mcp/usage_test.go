package mcp

import (
	"strings"
	"testing"
)

// TestUsageDocumentsEveryTool keeps usage.md coherent with the advertised tool
// set: a renamed or added tool that the manual does not mention fails here.
func TestUsageDocumentsEveryTool(t *testing.T) {
	tools := toolsList()
	if len(tools) == 0 {
		t.Fatal("toolsList returned no tools")
	}
	for _, tl := range tools {
		name, _ := tl["name"].(string)
		if !strings.Contains(usageMarkdown, name) {
			t.Errorf("usage.md does not document tool %q", name)
		}
	}
	// Result fields, knobs and error codes that agents depend on must stay
	// documented (ADR-0002: the recovery table is read by code).
	for _, term := range []string{
		"prefixes", "has_more", "prefix_count",
		"offset", "IPINFO_TOKEN", "update_db",
		"invalid_arguments", "missing_argument", "one_of",
		CodeNoDatabase, CodeDatabaseError, CodeNoToken, CodeUpdateFailed,
	} {
		if !strings.Contains(usageMarkdown, term) {
			t.Errorf("usage.md missing key term %q", term)
		}
	}
}
