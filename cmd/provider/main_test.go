package main

import (
	"reflect"
	"testing"
	"time"

	providerpkg "example.com/minikube-externalgrpc-autoscaler-demo/pkg/provider"
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
		verbosity:       1,
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
		"--v=4",
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
		verbosity:       4,
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
		"negative v":     {"--v=-1"},
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
	blocked := make(chan struct{})
	forced := make(chan struct{}, 1)
	returned := make(chan struct{})
	go func() {
		stopWithTimeout(
			func() { <-blocked },
			func() {
				forced <- struct{}{}
				close(blocked)
			},
			time.Millisecond,
		)
		close(returned)
	}()

	select {
	case <-forced:
	case <-time.After(time.Second):
		t.Fatal("force stop was not called")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("stopWithTimeout did not return")
	}
}
