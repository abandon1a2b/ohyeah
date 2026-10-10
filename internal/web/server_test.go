package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/config"
)

func TestShutdownWithIdleKeepAlive(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, &app.App{Config: config.Config{Server: config.ServerConfig{Address: address}}}, address)
	}()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/api/v1/health")
		if err == nil {
			var buffer [128]byte
			response.Body.Read(buffer[:])
			response.Body.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("HTTP service did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("idle keep-alive prevented shutdown")
	}
	probe, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP listener leaked: %v", err)
	}
	probe.Close()
}
