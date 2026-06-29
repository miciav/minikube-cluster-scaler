package provider

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"

	"example.com/minikube-externalgrpc-autoscaler-demo/pkg/minikube"
	protos "example.com/minikube-externalgrpc-autoscaler-demo/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	NodeGroup       string
	MinNodes        int32
	MaxNodes        int32
	DryRun          bool
	EnableScaleDown bool
}

type Provider struct {
	protos.UnimplementedCloudProviderServer
	mu      sync.Mutex
	scaleMu sync.Mutex
	cfg     Config
	client  *minikube.Client
	logger  *log.Logger
	nodes   []corev1.Node
	target  int32
}

func New(cfg Config, client *minikube.Client, logger *log.Logger) (*Provider, error) {
	if cfg.NodeGroup == "" {
		return nil, fmt.Errorf("node group is required")
	}
	if cfg.MinNodes < 0 {
		return nil, fmt.Errorf("minimum nodes must not be negative")
	}
	if cfg.MaxNodes < cfg.MinNodes {
		return nil, fmt.Errorf("maximum nodes must be at least minimum nodes")
	}
	if client == nil {
		return nil, fmt.Errorf("minikube client is required")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Provider{cfg: cfg, client: client, logger: logger}, nil
}

func (p *Provider) group() *protos.NodeGroup {
	return &protos.NodeGroup{
		Id:      p.cfg.NodeGroup,
		MinSize: p.cfg.MinNodes,
		MaxSize: p.cfg.MaxNodes,
		Debug:   p.cfg.NodeGroup,
	}
}

func (p *Provider) Refresh(ctx context.Context, _ *protos.RefreshRequest) (*protos.RefreshResponse, error) {
	nodes, err := p.client.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh nodes: %w", err)
	}

	observed := int32(len(nodes))
	p.mu.Lock()
	p.nodes = append([]corev1.Node(nil), nodes...)
	if !p.cfg.DryRun || p.target < observed {
		p.target = observed
	}
	p.mu.Unlock()
	p.logger.Printf("refreshed %d nodes", observed)
	return &protos.RefreshResponse{}, nil
}

func (p *Provider) NodeGroups(context.Context, *protos.NodeGroupsRequest) (*protos.NodeGroupsResponse, error) {
	return &protos.NodeGroupsResponse{NodeGroups: []*protos.NodeGroup{p.group()}}, nil
}

func (p *Provider) NodeGroupForNode(_ context.Context, req *protos.NodeGroupForNodeRequest) (*protos.NodeGroupForNodeResponse, error) {
	if req == nil || req.Node == nil {
		return &protos.NodeGroupForNodeResponse{NodeGroup: &protos.NodeGroup{}}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, node := range p.nodes {
		if req.Node.Name != "" && req.Node.Name == node.Name ||
			req.Node.ProviderID != "" && req.Node.ProviderID == node.Spec.ProviderID {
			return &protos.NodeGroupForNodeResponse{NodeGroup: p.group()}, nil
		}
	}
	return &protos.NodeGroupForNodeResponse{NodeGroup: &protos.NodeGroup{}}, nil
}

func (p *Provider) NodeGroupTargetSize(_ context.Context, req *protos.NodeGroupTargetSizeRequest) (*protos.NodeGroupTargetSizeResponse, error) {
	if req == nil || req.Id != p.cfg.NodeGroup {
		return nil, status.Error(codes.NotFound, "unknown node group")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return &protos.NodeGroupTargetSizeResponse{TargetSize: p.target}, nil
}

func (p *Provider) NodeGroupIncreaseSize(ctx context.Context, req *protos.NodeGroupIncreaseSizeRequest) (*protos.NodeGroupIncreaseSizeResponse, error) {
	p.scaleMu.Lock()
	defer p.scaleMu.Unlock()

	var group string
	var delta int32
	if req != nil {
		group, delta = req.Id, req.Delta
	}
	p.mu.Lock()
	current := p.target
	p.logger.Printf("scale-up group=%s delta=%d current=%d dry-run=%t", group, delta, current, p.cfg.DryRun)
	if req == nil || group != p.cfg.NodeGroup {
		p.mu.Unlock()
		return nil, status.Error(codes.NotFound, "unknown node group")
	}
	if delta <= 0 {
		p.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "increase delta must be positive")
	}
	if delta > p.cfg.MaxNodes-current {
		p.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "increase exceeds maximum node group size")
	}
	if p.cfg.DryRun {
		p.target += delta
		p.mu.Unlock()
		return &protos.NodeGroupIncreaseSizeResponse{}, nil
	}
	p.mu.Unlock()

	for range delta {
		if err := p.client.AddNode(ctx); err != nil {
			return nil, status.Errorf(codes.Internal, "add minikube node: %v", err)
		}
		nodes, err := p.client.Nodes(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "refresh nodes after scale-up: %v", err)
		}
		p.mu.Lock()
		p.nodes = append([]corev1.Node(nil), nodes...)
		p.target = int32(len(nodes))
		p.mu.Unlock()
	}
	return &protos.NodeGroupIncreaseSizeResponse{}, nil
}

func (p *Provider) NodeGroupNodes(_ context.Context, req *protos.NodeGroupNodesRequest) (*protos.NodeGroupNodesResponse, error) {
	if req == nil || req.Id != p.cfg.NodeGroup {
		return nil, status.Error(codes.NotFound, "unknown node group")
	}
	p.mu.Lock()
	nodes := append([]corev1.Node(nil), p.nodes...)
	p.mu.Unlock()

	instances := make([]*protos.Instance, len(nodes))
	for i, node := range nodes {
		id := node.Spec.ProviderID
		if id == "" {
			id = node.Name
		}
		instances[i] = &protos.Instance{
			Id: id,
			Status: &protos.InstanceStatus{
				InstanceState: protos.InstanceStatus_instanceRunning,
			},
		}
	}
	return &protos.NodeGroupNodesResponse{Instances: instances}, nil
}
