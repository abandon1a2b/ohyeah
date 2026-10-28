package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWorkersCancelAndJoinOnFailure(t *testing.T) {
	failure := errors.New("listener failed")
	started := make(chan struct{})
	finished := make(chan struct{})
	err := runWorkers(context.Background(), []worker{
		{"listener", func(context.Context) error { <-started; return failure }},
		{"background", func(ctx context.Context) error { close(started); <-ctx.Done(); close(finished); return ctx.Err() }},
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "listener") {
		t.Fatalf("error=%v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("returned before worker cleanup")
	}
}

func TestWorkersShutdownAndUnexpectedExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWorkers(ctx, []worker{{"background", func(ctx context.Context) error { return ctx.Err() }}}); err != nil {
		t.Fatal(err)
	}
	if err := runWorkers(context.Background(), []worker{{"listener", func(context.Context) error { return nil }}}); err == nil {
		t.Fatal("unexpected successful exit was hidden")
	}
	if err := runWorkers(context.Background(), []worker{{"listener", func(context.Context) error { return context.DeadlineExceeded }}}); err == nil {
		t.Fatal("worker timeout was hidden")
	}
}
