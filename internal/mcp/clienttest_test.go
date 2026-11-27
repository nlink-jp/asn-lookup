package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nlink-jp/mcp"
	"github.com/nlink-jp/mcp/client"
	"github.com/nlink-jp/mcp/probe"

	"github.com/nlink-jp/asn-lookup/internal/engine"
)

// open serves e over pipes and opens it with nlink-jp/mcp's client, the way
// a dual-era MCP client does (server-migration §6). Nothing here writes the
// handshake by hand.
func open(t *testing.T, e *engine.Engine) *client.Client {
	t.Helper()
	return openWith(t, e, client.Options{})
}

// openWith is open with the client's options (CancelNotice, say).
func openWith(t *testing.T, e *engine.Engine, opts client.Options) *client.Client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, e, "test-ver", inR, outW)
		_ = outW.Close()
	}()
	kill := func() { cancel(); _ = inW.Close(); _ = outR.Close() }
	conn, err := client.NewLineConn(outR, inW, kill, client.LineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Registered before Open, so a failed open leaves no goroutine or pipe.
	var cl *client.Client
	t.Cleanup(func() {
		if cl == nil {
			kill()
			return
		}
		_ = cl.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after the client closed")
		}
		kill()
	})
	octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ocancel()
	opts.Client = mcp.Implementation{Name: "test", Version: "0"}
	c, err := client.Open(octx, conn, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cl = c
	return cl
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
	var c contentItem
	if err := json.Unmarshal(res.Content[0], &c); err != nil {
		t.Fatalf("%s: content: %v", tool, err)
	}
	return c.Text, res.IsError
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

// rpcCode is the JSON-RPC error code of err, or 0.
func rpcCode(err error) int {
	var e *mcp.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// ADR-030 §7: the server against the three probe clients — legacy only,
// dual-era, modern only — with the outcomes the specification's
// compatibility matrix gives for a dual-era server.
func TestAgainstTheProbeClients(t *testing.T) {
	for _, kind := range []probe.ClientKind{probe.ClientLegacyOnly, probe.ClientDual, probe.ClientModernOnly} {
		e := newEngine(t, true)
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = Serve(ctx, e, "test-ver", inR, outW); _ = outW.Close() }()
		r := probe.Run(ctx, kind, outR, inW, probe.ClientOptions{StepTimeout: 2 * time.Second})
		cancel()
		_ = inW.Close()
		if want := probe.Expect(kind, probe.ServerDual); r.Outcome != want || len(r.Violations) != 0 || len(r.Warnings) != 0 {
			t.Errorf("%v probe client: outcome %s (want %s), violations %v, warnings %v, error %s",
				kind, r.Outcome, want, r.Violations, r.Warnings, r.Error)
		}
	}
}
