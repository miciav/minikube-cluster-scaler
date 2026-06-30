package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/miciav/minikube-cluster-scaler/pkg/minikube"
	protos "github.com/miciav/minikube-cluster-scaler/proto"
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
	mu            sync.Mutex
	gate          chan struct{}
	cfg           Config
	client        *minikube.Client
	logger        *log.Logger
	nodes         []corev1.Node
	target        int32
	dryRunDeleted map[string]struct{}
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
	p := &Provider{cfg: cfg, client: client, logger: logger, gate: make(chan struct{}, 1)}
	p.gate <- struct{}{}
	return p, nil
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
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer p.release()
	if err := p.refresh(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, fmt.Errorf("refresh nodes: %w", err)
	}
	return &protos.RefreshResponse{}, nil
}

// refresh requires the operation gate.
func (p *Provider) refresh(ctx context.Context) error {
	nodes, err := p.client.Nodes(ctx)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if p.cfg.DryRun && len(p.dryRunDeleted) != 0 {
		kept := nodes[:0]
		for _, node := range nodes {
			if _, deleted := p.dryRunDeleted[node.Name]; !deleted {
				kept = append(kept, node)
			}
		}
		nodes = kept
	}
	observed := int32(len(nodes))
	p.nodes = append([]corev1.Node(nil), nodes...)
	if !p.cfg.DryRun || p.target < observed {
		p.target = observed
	}
	p.mu.Unlock()
	p.logger.Printf("refreshed %d nodes", observed)
	return nil
}

func (p *Provider) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-p.gate:
		return nil
	}
}

func (p *Provider) release() {
	p.gate <- struct{}{}
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
	var group string
	var delta int32
	if req != nil {
		group, delta = req.Id, req.Delta
	}
	fail := func(err error, progress int32) (*protos.NodeGroupIncreaseSizeResponse, error) {
		p.mu.Lock()
		current := p.target
		p.mu.Unlock()
		p.logger.Printf("scale-up failed group=%s delta=%d current=%d progress=%d/%d dry-run=%t error=%v", group, delta, current, progress, delta, p.cfg.DryRun, err)
		return nil, err
	}
	if err := p.acquire(ctx); err != nil {
		return fail(err, 0)
	}
	defer p.release()

	p.mu.Lock()
	current := p.target
	p.mu.Unlock()
	p.logger.Printf("scale-up request group=%s delta=%d current=%d dry-run=%t", group, delta, current, p.cfg.DryRun)
	if req == nil || group != p.cfg.NodeGroup {
		return fail(status.Error(codes.NotFound, "unknown node group"), 0)
	}
	if delta <= 0 {
		return fail(status.Error(codes.InvalidArgument, "increase delta must be positive"), 0)
	}
	if p.cfg.DryRun {
		p.mu.Lock()
		current = p.target
		if delta > p.cfg.MaxNodes-current {
			p.mu.Unlock()
			return fail(status.Error(codes.FailedPrecondition, "increase exceeds maximum node group size"), 0)
		}
		p.target += delta
		current = p.target
		p.mu.Unlock()
		p.logger.Printf("scale-up succeeded group=%s delta=%d target=%d dry-run=true", group, delta, current)
		return &protos.NodeGroupIncreaseSizeResponse{}, nil
	}

	if err := p.refresh(ctx); err != nil {
		if ctx.Err() != nil {
			return fail(status.FromContextError(ctx.Err()).Err(), 0)
		}
		return fail(status.Errorf(codes.Internal, "refresh nodes before scale-up: %v", err), 0)
	}
	p.mu.Lock()
	current = p.target
	p.mu.Unlock()
	if delta > p.cfg.MaxNodes-current {
		return fail(status.Error(codes.FailedPrecondition, "increase exceeds maximum node group size"), 0)
	}

	var progress int32
	for range delta {
		if err := p.client.AddNode(ctx); err != nil {
			if ctx.Err() != nil {
				return fail(status.FromContextError(ctx.Err()).Err(), progress)
			}
			return fail(status.Errorf(codes.Internal, "add minikube node: %v", err), progress)
		}
		if err := p.refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return fail(status.FromContextError(ctx.Err()).Err(), progress)
			}
			return fail(status.Errorf(codes.Internal, "refresh nodes after scale-up: %v", err), progress)
		}
		progress++
	}
	p.mu.Lock()
	current = p.target
	p.mu.Unlock()
	p.logger.Printf("scale-up succeeded group=%s delta=%d target=%d dry-run=false", group, delta, current)
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

func (p *Provider) NodeGroupTemplateNodeInfo(_ context.Context, req *protos.NodeGroupTemplateNodeInfoRequest) (*protos.NodeGroupTemplateNodeInfoResponse, error) {
	if req == nil || req.Id != p.cfg.NodeGroup {
		return nil, status.Error(codes.NotFound, "unknown node group")
	}

	p.mu.Lock()
	if len(p.nodes) == 0 {
		p.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "no observed node")
	}
	observed := p.nodes[0]
	template := corev1.Node{
		Status: corev1.NodeStatus{
			Capacity:    observed.Status.Capacity.DeepCopy(),
			Allocatable: observed.Status.Allocatable.DeepCopy(),
		},
	}
	template.Labels = map[string]string{}
	for _, label := range []string{"kubernetes.io/arch", "kubernetes.io/os", "node.kubernetes.io/instance-type"} {
		if value, ok := observed.Labels[label]; ok {
			template.Labels[label] = value
		}
	}
	p.mu.Unlock()

	nodeBytes, err := template.Marshal()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal node template: %v", err)
	}
	return &protos.NodeGroupTemplateNodeInfoResponse{NodeBytes: nodeBytes}, nil
}

func (p *Provider) NodeGroupDeleteNodes(ctx context.Context, req *protos.NodeGroupDeleteNodesRequest) (*protos.NodeGroupDeleteNodesResponse, error) {
	var group string
	if req != nil {
		group = req.Id
	}
	fail := func(err error) (*protos.NodeGroupDeleteNodesResponse, error) {
		p.logger.Printf("scale-down failed group=%s dry-run=%t error=%v", group, p.cfg.DryRun, err)
		return nil, err
	}
	contextStatus := func(err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return status.FromContextError(ctxErr).Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return status.FromContextError(err).Err()
		}
		return nil
	}
	if group != p.cfg.NodeGroup {
		err := status.Error(codes.NotFound, "unknown node group")
		p.logger.Printf("scale-down rejected group=%s error=%v", group, err)
		return nil, err
	}
	if !p.cfg.EnableScaleDown {
		err := status.Error(codes.FailedPrecondition, "scale-down disabled in this demo")
		p.logger.Printf("scale-down rejected group=%s error=%v", group, err)
		return nil, err
	}
	if len(req.Nodes) != 1 {
		return fail(status.Error(codes.InvalidArgument, "exactly one node must be deleted"))
	}
	if err := p.acquire(ctx); err != nil {
		return fail(err)
	}
	defer p.release()
	if err := p.refresh(ctx); err != nil {
		if contextErr := contextStatus(err); contextErr != nil {
			return fail(contextErr)
		}
		return fail(status.Errorf(codes.Internal, "refresh nodes before scale-down: %v", err))
	}

	p.mu.Lock()
	if int32(len(p.nodes)) <= p.cfg.MinNodes {
		p.mu.Unlock()
		return fail(status.Error(codes.FailedPrecondition, "deletion would breach minimum node group size"))
	}
	requested := req.Nodes[0]
	index := -1
	if requested != nil {
		for i, node := range p.nodes {
			if requested.Name != "" && requested.Name == node.Name || requested.ProviderID != "" && requested.ProviderID == node.Spec.ProviderID {
				index = i
				break
			}
		}
	}
	if index < 0 {
		p.mu.Unlock()
		return fail(status.Error(codes.NotFound, "node is not observed"))
	}
	node := p.nodes[index]
	if node.Name == "" {
		p.mu.Unlock()
		return fail(status.Error(codes.FailedPrecondition, "node has no Kubernetes name"))
	}
	if _, controlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; controlPlane {
		p.mu.Unlock()
		return fail(status.Error(codes.FailedPrecondition, "control-plane node cannot be deleted"))
	}
	if p.cfg.DryRun {
		if p.dryRunDeleted == nil {
			p.dryRunDeleted = make(map[string]struct{})
		}
		p.dryRunDeleted[node.Name] = struct{}{}
		p.nodes = append(p.nodes[:index], p.nodes[index+1:]...)
		p.target--
		target := p.target
		p.mu.Unlock()
		p.logger.Printf("scale-down succeeded group=%s node=%s target=%d dry-run=true", group, node.Name, target)
		return &protos.NodeGroupDeleteNodesResponse{}, nil
	}
	p.mu.Unlock()

	if err := p.client.DeleteNode(ctx, node.Name); err != nil {
		if contextErr := contextStatus(err); contextErr != nil {
			return fail(contextErr)
		}
		return fail(status.Errorf(codes.Internal, "delete minikube node: %v", err))
	}
	if err := p.refresh(ctx); err != nil {
		if contextErr := contextStatus(err); contextErr != nil {
			return fail(contextErr)
		}
		return fail(status.Errorf(codes.Internal, "refresh nodes after scale-down: %v", err))
	}
	p.mu.Lock()
	for _, observed := range p.nodes {
		if observed.Name == node.Name {
			p.mu.Unlock()
			return fail(status.Errorf(codes.Internal, "deleted node %q is still observed", node.Name))
		}
	}
	target := p.target
	p.mu.Unlock()
	p.logger.Printf("scale-down succeeded group=%s node=%s target=%d dry-run=false", group, node.Name, target)
	return &protos.NodeGroupDeleteNodesResponse{}, nil
}

func (p *Provider) NodeGroupDecreaseTargetSize(_ context.Context, req *protos.NodeGroupDecreaseTargetSizeRequest) (*protos.NodeGroupDecreaseTargetSizeResponse, error) {
	var group string
	if req != nil {
		group = req.Id
	}
	return nil, p.rejectScaleDown(group)
}

func (p *Provider) rejectScaleDown(group string) error {
	var err error
	switch {
	case group != p.cfg.NodeGroup:
		err = status.Error(codes.NotFound, "unknown node group")
	case !p.cfg.EnableScaleDown:
		err = status.Error(codes.FailedPrecondition, "scale-down disabled in this demo")
	default:
		err = status.Error(codes.Unimplemented, "scale-down is not implemented in this demo")
	}
	p.logger.Printf("scale-down rejected group=%s error=%v", group, err)
	return err
}

func (p *Provider) GPULabel(context.Context, *protos.GPULabelRequest) (*protos.GPULabelResponse, error) {
	return &protos.GPULabelResponse{}, nil
}

func (p *Provider) GetAvailableGPUTypes(context.Context, *protos.GetAvailableGPUTypesRequest) (*protos.GetAvailableGPUTypesResponse, error) {
	return &protos.GetAvailableGPUTypesResponse{}, nil
}

func (p *Provider) Cleanup(context.Context, *protos.CleanupRequest) (*protos.CleanupResponse, error) {
	return &protos.CleanupResponse{}, nil
}
