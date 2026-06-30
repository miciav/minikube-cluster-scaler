package minikube

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNodesUsesProfileContextAndDecodesNodes(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := New("demo", time.Second, log.New(io.Discard, "", 0), func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return []byte(`{"items":[{"metadata":{"name":"demo"}}]}`), nil, nil
	})

	nodes, err := c.Nodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].Name != "demo" {
		t.Fatalf("Nodes() = %#v, %v", nodes, err)
	}
	if gotName != "kubectl" || !slices.Equal(gotArgs, []string{"--context", "demo", "get", "nodes", "-o", "json"}) {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
}

func TestAddNodeUsesArgumentArray(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := New("demo", time.Second, nil, func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return nil, nil, nil
	})

	if err := c.AddNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotName != "minikube" || !slices.Equal(gotArgs, []string{"node", "add", "-p", "demo"}) {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
}

func TestDeleteNodeUsesArgumentArray(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := New("demo", time.Second, nil, func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return nil, nil, nil
	})

	if err := c.DeleteNode(context.Background(), "demo-m02"); err != nil {
		t.Fatal(err)
	}
	if gotName != "minikube" || !slices.Equal(gotArgs, []string{"node", "delete", "demo-m02", "-p", "demo"}) {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
}

func TestCommandFailureIncludesOutputAndWrapsCause(t *testing.T) {
	cause := errors.New("exit 1")
	c := New("demo", time.Second, nil, func(context.Context, string, ...string) ([]byte, []byte, error) {
		return []byte("partial output"), []byte("node creation failed"), cause
	})

	err := c.AddNode(context.Background())
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
	for _, text := range []string{`minikube ["node" "add" "-p" "demo"]`, "partial output", "node creation failed"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("error %q does not contain %q", err, text)
		}
	}
}

func TestCommandTimeout(t *testing.T) {
	c := New("demo", time.Millisecond, nil, func(ctx context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, errors.New("signal: killed")
	})

	err := c.AddNode(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandIsLogged(t *testing.T) {
	var logs bytes.Buffer
	c := New("demo", time.Second, log.New(&logs, "", 0), func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, nil, nil
	})

	if err := c.AddNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logs.String() != "exec: minikube [\"node\" \"add\" \"-p\" \"demo\"]\n" {
		t.Fatalf("logs = %q", logs.String())
	}
}
