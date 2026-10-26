package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	providerpkg "github.com/miciav/minikube-cluster-scaler/pkg/provider"
)

func TestParseFlagsDefaults(t *testing.T) {
	got, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := options{
		listen:          "0.0.0.0:9090",
		profile:         "autoscaling-demo",
		nodeGroup:       "minikube-workers",
		minNodes:        1,
		maxNodes:        3,
		dryRun:          false,
		enableScaleDown: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFlags() = %+v, want %+v", got, want)
	}
}

func TestParseFlagsOverridesEveryFlag(t *testing.T) {
	got, err := parseFlags([]string{
		"--listen=127.0.0.1:1234",
		"--profile=other",
		"--node-group=workers",
		"--min-nodes=2",
		"--max-nodes=5",
		"--dry-run=true",
		"--enable-scale-down=true",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := options{
		listen:          "127.0.0.1:1234",
		profile:         "other",
		nodeGroup:       "workers",
		minNodes:        2,
		maxNodes:        5,
		dryRun:          true,
		enableScaleDown: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFlags() = %+v, want %+v", got, want)
	}
}

func TestParseFlagsRejectsInvalidOptions(t *testing.T) {
	tests := map[string][]string{
		"empty listen":   {"--listen="},
		"empty profile":  {"--profile="},
		"empty group":    {"--node-group="},
		"negative min":   {"--min-nodes=-1"},
		"max before min": {"--min-nodes=4", "--max-nodes=3"},
		"min overflow":   {"--min-nodes", "2147483648"},
		"max overflow":   {"--max-nodes", "2147483648"},
		"unknown flag":   {"--wat"},
		"positional arg": {"extra"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFlags(args); err == nil {
				t.Fatalf("parseFlags(%q) returned nil error", args)
			}
		})
	}
}

func TestProviderConfig(t *testing.T) {
	opts := options{
		nodeGroup:       "workers",
		minNodes:        0,
		maxNodes:        2147483647,
		dryRun:          true,
		enableScaleDown: true,
	}
	want := providerpkg.Config{
		NodeGroup:       "workers",
		MinNodes:        0,
		MaxNodes:        2147483647,
		DryRun:          true,
		EnableScaleDown: true,
	}
	if got := opts.providerConfig(); got != want {
		t.Fatalf("providerConfig() = %+v, want %+v", got, want)
	}
}

func TestStopWithTimeoutForcesBlockedGracefulStop(t *testing.T) {
	cleanup := make(chan struct{})
	gracefulReturned := make(chan struct{})
	forced := make(chan struct{}, 1)
	returned := make(chan struct{})
	go func() {
		stopWithTimeout(
			func() {
				<-cleanup
				close(gracefulReturned)
			},
			func() { forced <- struct{}{} },
			time.Millisecond,
		)
		close(returned)
	}()

	select {
	case <-forced:
	case <-time.After(time.Second):
		close(cleanup)
		<-gracefulReturned
		<-returned
		t.Fatal("force stop was not called")
	}
	returnedPromptly := true
	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		returnedPromptly = false
	}
	close(cleanup)
	<-gracefulReturned
	<-returned
	if !returnedPromptly {
		t.Fatal("stopWithTimeout waited for graceful stop after forcing")
	}
}

// fakeKubectl puts a kubectl on PATH that runs the given shell body.
func fakeKubectl(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test fake must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunServesUntilContextCanceled(t *testing.T) {
	fakeKubectl(t, `printf '%s\n' '{"items":[{"metadata":{"name":"demo"}}]}'`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs syncBuffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"--listen=127.0.0.1:0"}, &logs) }()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "starting provider") {
		if time.Now().After(deadline) {
			t.Fatalf("provider did not start, logs: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	err := run(context.Background(), []string{"--wat"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "flags:") {
		t.Fatalf("run() = %v, want flags error", err)
	}
}

func TestRunFailsInitialRefresh(t *testing.T) {
	fakeKubectl(t, "exit 1")
	err := run(context.Background(), []string{"--listen=127.0.0.1:0"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "initial refresh") {
		t.Fatalf("run() = %v, want initial refresh error", err)
	}
}

func TestRunFailsToListen(t *testing.T) {
	fakeKubectl(t, `printf '%s\n' '{"items":[]}'`)
	busy, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	err = run(context.Background(), []string{"--listen=" + busy.Addr().String()}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("run() = %v, want listen error", err)
	}
}
