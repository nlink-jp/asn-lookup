package mcp

// The codes of asn-lookup's tool errors (ADR-0002), beside mcptool's own
// invalid_arguments, missing_argument and internal_error. usage.md's recovery
// table lists every one; usage_test.go pins that.
const (
	CodeNoDatabase    = "no_database"    // the index has not been built
	CodeDatabaseError = "database_error" // the index exists but cannot be read
	CodeNoToken       = "no_token"       // update_db without an ipinfo token
	CodeUpdateFailed  = "update_failed"  // update_db could not fetch or rebuild
)
