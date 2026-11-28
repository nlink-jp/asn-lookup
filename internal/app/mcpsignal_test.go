package app

import (
	"context"
	"io"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/nlink-jp/mcptool/serve"
)

// SIGINT and SIGTERM end the MCP server through its ctx, even while stdin is
// open (server-migration §2); both are registered (mcptool's serve.Run, ADR-0002). Served on context.Background(), a signal
// killed the process instead, and this waits in vain.
func TestASignalEndsTheMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	defer func(orig func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)) {
		serve.NotifyContext = orig
	}(serve.NotifyContext)
	var registered []os.Signal
	serve.NotifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		registered = sigs
		ctx, cancel := context.WithCancel(parent)
		go func() { time.Sleep(100 * time.Millisecond); cancel() }() // the signal arrives
		return ctx, cancel
	}
	inR, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	defer func(o serve.Options) { mcpServe = o }(mcpServe)
	mcpServe = serve.Options{Stdin: inR, Stdout: io.Discard}
	done := make(chan int, 1)
	go func() { done <- cmdMCP(nil, "test") }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the MCP server did not end on the signal")
	}
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !slices.Contains(registered, want) {
			t.Errorf("%v is not among the signals that end the server: %v", want, registered)
		}
	}
}
