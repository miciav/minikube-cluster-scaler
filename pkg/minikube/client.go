// Package minikube runs the minikube and kubectl commands the provider needs.
package minikube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// RunFunc executes a command and returns its stdout and stderr; tests replace it.
type RunFunc func(context.Context, string, ...string) ([]byte, []byte, error)

// Client runs commands against one minikube profile and its kubectl context.
type Client struct {
	profile string
	timeout time.Duration
	run     RunFunc
	logger  *log.Logger
}

// New returns a Client; a nil logger discards output and a nil run executes real commands.
func New(profile string, timeout time.Duration, logger *log.Logger, run RunFunc) *Client {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if run == nil {
		run = runCommand
	}
	return &Client{profile: profile, timeout: timeout, run: run, logger: logger}
}

// Nodes returns the nodes of the profile's cluster as reported by kubectl.
func (c *Client) Nodes(ctx context.Context) ([]corev1.Node, error) {
	stdout, err := c.exec(ctx, "kubectl", "--context", c.profile, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}

	var list corev1.NodeList
	if err := json.Unmarshal(stdout, &list); err != nil {
		return nil, fmt.Errorf("decode kubectl nodes: %w", err)
	}
	return list.Items, nil
}

// AddNode adds one worker node to the profile.
func (c *Client) AddNode(ctx context.Context) error {
	_, err := c.exec(ctx, "minikube", "node", "add", "-p", c.profile)
	return err
}

// DeleteNode deletes the named node from the profile.
func (c *Client) DeleteNode(ctx context.Context, name string) error {
	_, err := c.exec(ctx, "minikube", "node", "delete", name, "-p", c.profile)
	return err
}

func (c *Client) exec(parent context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	c.logger.Printf("exec: %s %q", name, args)
	stdout, stderr, err := c.run(ctx, name, args...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s %q: %w: process error=%w: stdout=%q stderr=%q", name, args, ctxErr, err, stdout, stderr)
		}
		return nil, fmt.Errorf("%s %q: %w: stdout=%q stderr=%q", name, args, err, stdout, stderr)
	}
	return stdout, nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed binaries and argv from this package, never a shell
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
