package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nlink-jp/mcp/client"
	"github.com/nlink-jp/mcptool"
	"github.com/nlink-jp/mcptool/tooltest"

	"github.com/nlink-jp/asn-lookup/internal/engine"
)

// build returns a function that serves e, for mcptool's tooltest (a server
// is served once, so each open builds a fresh one over the same engine).
func build(e *engine.Engine) tooltest.NewServer {
	return func(t testing.TB) *mcptool.Server {
		t.Helper()
		s, err := New(e, "test-ver")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

// open serves e over a pipe and opens it with nlink-jp/mcp's client.
func open(t *testing.T, e *engine.Engine) *client.Client {
	t.Helper()
	return tooltest.Open(t, build(e))
}

// openWith is open with the client's options (CancelNotice, say).
func openWith(t *testing.T, e *engine.Engine, opts client.Options) *client.Client {
	t.Helper()
	return tooltest.OpenWith(t, build(e), opts)
}

// call runs one tool. args is the arguments' JSON, or "" for none. It
// returns the first content block's text and whether the tool failed.
func call(t *testing.T, cl *client.Client, tool, args string) (string, bool) {
	t.Helper()
	res, err := callErr(t, cl, tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: empty content", tool)
	}
	var c struct{ Text string }
	if err := json.Unmarshal(res.Content[0], &c); err != nil {
		t.Fatalf("%s: content: %v", tool, err)
	}
	return c.Text, res.IsError
}

// toolErr runs one tool that must fail and returns its structured error.
func toolErr(t *testing.T, cl *client.Client, tool, args string) *mcptool.Error {
	t.Helper()
	res, err := callErr(t, cl, tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	e, ok := tooltest.ToolError(res)
	if !ok {
		t.Fatalf("%s: not a structured tool error: %+v", tool, res)
	}
	return e
}

// callErr runs one tool and returns the protocol error, if any.
func callErr(t *testing.T, cl *client.Client, tool, args string) (*client.ToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var a any
	if args != "" {
		a = json.RawMessage(args)
	}
	return cl.CallTool(ctx, tool, a, nil)
}

// ADR-030 §7: the server against the three probe clients.
func TestAgainstTheProbeClients(t *testing.T) {
	tooltest.ProbeMatrix(t, build(newEngine(t, true)))
}

// ADR-021 §10: no tool returns a path that needs work_dir, and no schema or
// description holds a retired spelling. (Closed schemas are checked when the
// tools are registered.)
func TestTheSchemaContract(t *testing.T) {
	s, err := New(newEngine(t, true), "test-ver")
	if err != nil {
		t.Fatal(err)
	}
	tooltest.SchemaContract(t, s, tooltest.Contract{})
	if len(s.Tools()) != len(toolsList()) {
		t.Errorf("registered %d tools of %d", len(s.Tools()), len(toolsList()))
	}
}
