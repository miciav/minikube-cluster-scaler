package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/minikube-externalgrpc-autoscaler-demo/pkg/minikube"
	providerpkg "example.com/minikube-externalgrpc-autoscaler-demo/pkg/provider"
	protos "example.com/minikube-externalgrpc-autoscaler-demo/proto"
	"google.golang.org/grpc"
)

const shutdownGracePeriod = 5 * time.Second

type options struct {
	listen          string
	profile         string
	nodeGroup       string
	minNodes        int64
	maxNodes        int64
	dryRun          bool
	enableScaleDown bool
	verbosity       int
}

func parseFlags(args []string) (options, error) {
	opts := options{}
	flags := flag.NewFlagSet("provider", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.listen, "listen", "0.0.0.0:9090", "TCP listen address")
	flags.StringVar(&opts.profile, "profile", "autoscaling-demo", "minikube profile")
	flags.StringVar(&opts.nodeGroup, "node-group", "minikube-workers", "node group name")
	flags.Int64Var(&opts.minNodes, "min-nodes", 1, "minimum node count")
	flags.Int64Var(&opts.maxNodes, "max-nodes", 3, "maximum node count")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "simulate scaling without changing minikube")
	flags.BoolVar(&opts.enableScaleDown, "enable-scale-down", false, "enable the scale-down boundary")
	flags.IntVar(&opts.verbosity, "v", 1, "log verbosity")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %q", flags.Args())
	}
	if opts.listen == "" {
		return options{}, fmt.Errorf("listen address is required")
	}
	if opts.profile == "" {
		return options{}, fmt.Errorf("profile is required")
	}
	if opts.nodeGroup == "" {
		return options{}, fmt.Errorf("node group is required")
	}
	if opts.minNodes < 0 {
		return options{}, fmt.Errorf("minimum nodes must not be negative")
	}
	if opts.maxNodes < opts.minNodes {
		return options{}, fmt.Errorf("maximum nodes must be at least minimum nodes")
	}
	if opts.minNodes > math.MaxInt32 || opts.maxNodes > math.MaxInt32 {
		return options{}, fmt.Errorf("node bounds must fit int32")
	}
	if opts.verbosity < 0 {
		return options{}, fmt.Errorf("verbosity must not be negative")
	}
	return opts, nil
}

func (opts options) providerConfig() providerpkg.Config {
	return providerpkg.Config{
		NodeGroup:       opts.nodeGroup,
		MinNodes:        int32(opts.minNodes),
		MaxNodes:        int32(opts.maxNodes),
		DryRun:          opts.dryRun,
		EnableScaleDown: opts.enableScaleDown,
	}
}

func stopWithTimeout(gracefulStop, forceStop func(), timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		gracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		forceStop()
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	opts, err := parseFlags(args)
	if err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	logger := log.New(stderr, "", log.LstdFlags)
	client := minikube.New(opts.profile, 10*time.Minute, logger, nil)
	p, err := providerpkg.New(opts.providerConfig(), client, logger)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	if _, err := p.Refresh(ctx, &protos.RefreshRequest{}); err != nil {
		return fmt.Errorf("initial refresh: %w", err)
	}

	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.listen, err)
	}
	defer listener.Close()
	server := grpc.NewServer()
	protos.RegisterCloudProviderServer(server, p)
	logger.Printf("starting provider listen=%s profile=%s node-group=%s min-nodes=%d max-nodes=%d dry-run=%t enable-scale-down=%t v=%d",
		opts.listen, opts.profile, opts.nodeGroup, opts.minNodes, opts.maxNodes, opts.dryRun, opts.enableScaleDown, opts.verbosity)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return fmt.Errorf("gRPC server stopped unexpectedly")
	case <-ctx.Done():
		stopWithTimeout(server.GracefulStop, server.Stop, shutdownGracePeriod)
		<-serveErr
		return nil
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "provider: %v\n", err)
		os.Exit(1)
	}
}
