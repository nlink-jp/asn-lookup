// Command asn-lookup performs local IP-to-AS and ASN-to-prefix lookups from the
// IPinfo Lite database, as a CLI and a local MCP server.
package main

import (
	"os"

	"github.com/nlink-jp/asn-lookup/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
