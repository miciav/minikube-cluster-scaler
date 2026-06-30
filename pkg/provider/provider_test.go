package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miciav/minikube-cluster-scaler/pkg/minikube"
	protos "github.com/miciav/minikube-cluster-scaler/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestNewValidatesConfig(t *testing.T) {
	client := minikube.New("demo", time.Second, nil, func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nodeList(), nil, nil
	})
	tests := []struct {
		name    string
		cfg     Config
		client  *minikube.Client
		wantErr bool
	}{
		{name: "valid", cfg: Config{NodeGroup: "workers", MinNodes: 0, MaxNodes: 1}, client: client},
		{name: "empty group", cfg: Config{MaxNodes: 1}, client: client, wantErr: true},
		{name: "negative minimum", cfg: Config{NodeGroup: "workers", MinNodes: -1, MaxNodes: 1}, client: client, wantErr: true},
		{name: "maximum below minimum", cfg: Config{NodeGroup: "workers", MinNodes: 2, MaxNodes: 1}, client: client, wantErr: true},
		{name: "nil client", cfg: Config{NodeGroup: "workers", MaxNodes: 1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, tt.client, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRefreshExposesOneGroupAndObservedTarget(t *testing.T) {
	var logs bytes.Buffer
	p, err := New(
		Config{NodeGroup: "minikube-workers", MinNodes: 1, MaxNodes: 3},
		minikube.New("demo", time.Second, nil, nodeRunner(
			corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
		)),
		log.New(&logs, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}
	groups, err := p.NodeGroups(context.Background(), &protos.NodeGroupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups.NodeGroups) != 1 {
		t.Fatalf("groups = %#v", groups)
	}
	group := groups.NodeGroups[0]
	if group.Id != "minikube-workers" || group.MinSize != 1 || group.MaxSize != 3 || group.Debug != "minikube-workers" {
		t.Fatalf("group = %#v", group)
	}
	if again := p.group(); again.Id != group.Id || again.MinSize != group.MinSize || again.MaxSize != group.MaxSize || again.Debug != group.Debug {
		t.Fatalf("group changed: first=%#v again=%#v", group, again)
	}
	size, err := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if err != nil {
		t.Fatal(err)
	}
	if size.TargetSize != 1 {
		t.Fatalf("target size = %d", size.TargetSize)
	}
	if !strings.Contains(logs.String(), "refreshed 1 nodes") {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestNodeGroupForNodeMapsObservedNodes(t *testing.T) {
	p := testProvider(t, false, nodeRunner(
		corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
		corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Spec: corev1.NodeSpec{ProviderID: "minikube://worker"}},
	))
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		req  *protos.NodeGroupForNodeRequest
		want string
	}{
		{name: "hybrid control plane by name", req: &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{Name: "demo"}}, want: "minikube-workers"},
		{name: "worker by provider ID", req: &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{ProviderID: "minikube://worker"}}, want: "minikube-workers"},
		{name: "unknown", req: &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{Name: "foreign"}}},
		{name: "empty node", req: &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{}}},
		{name: "missing node", req: &protos.NodeGroupForNodeRequest{}},
		{name: "nil request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.NodeGroupForNode(context.Background(), tt.req)
			if err != nil {
				t.Fatal(err)
			}
			if got.NodeGroup == nil || got.NodeGroup.Id != tt.want {
				t.Fatalf("NodeGroupForNode() = %#v, want id %q", got, tt.want)
			}
		})
	}
}

func TestNodeGroupNodesReturnsRunningInstances(t *testing.T) {
	p := testProvider(t, false, nodeRunner(
		corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
		corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Spec: corev1.NodeSpec{ProviderID: "minikube://worker"}},
	))
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}

	got, err := p.NodeGroupNodes(context.Background(), &protos.NodeGroupNodesRequest{Id: "minikube-workers"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Instances) != 2 {
		t.Fatalf("instances = %#v", got.Instances)
	}
	for i, wantID := range []string{"demo", "minikube://worker"} {
		instance := got.Instances[i]
		if instance.Id != wantID || instance.Status == nil || instance.Status.InstanceState != protos.InstanceStatus_instanceRunning {
			t.Fatalf("instance %d = %#v", i, instance)
		}
	}
}

func TestNodeGroupTemplateNodeInfoReturnsSanitizedObservedNode(t *testing.T) {
	source := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo",
			UID:  types.UID("node-uid"),
			Labels: map[string]string{
				"kubernetes.io/arch":                    "amd64",
				"kubernetes.io/os":                      "linux",
				"node.kubernetes.io/instance-type":      "minikube",
				"kubernetes.io/hostname":                "demo",
				"node-role.kubernetes.io/control-plane": "",
				"example.com/custom":                    "value",
			},
			Annotations: map[string]string{"example.com/annotation": "value"},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "minikube://demo",
			Taints: []corev1.Taint{{
				Key:    "node-role.kubernetes.io/control-plane",
				Effect: corev1.TaintEffectNoSchedule,
			}},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("3500m"),
				corev1.ResourceMemory: resource.MustParse("7Gi"),
			},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.1"}},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo: corev1.NodeSystemInfo{
				MachineID:               "machine-id",
				SystemUUID:              "system-uuid",
				BootID:                  "boot-id",
				KernelVersion:           "kernel",
				OSImage:                 "os-image",
				ContainerRuntimeVersion: "container-runtime",
				KubeletVersion:          "kubelet",
				KubeProxyVersion:        "kube-proxy",
				OperatingSystem:         "linux",
				Architecture:            "amd64",
			},
		},
	}
	p := testProvider(t, false, nodeRunner(source))
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}

	template := templateNode(t, p)
	wantLabels := map[string]string{
		"kubernetes.io/arch":               "amd64",
		"kubernetes.io/os":                 "linux",
		"node.kubernetes.io/instance-type": "minikube",
	}
	if !reflect.DeepEqual(template.Labels, wantLabels) {
		t.Fatalf("labels = %v, want %v", template.Labels, wantLabels)
	}
	metadata := template.ObjectMeta.DeepCopy()
	metadata.Labels = nil
	if !reflect.DeepEqual(*metadata, metav1.ObjectMeta{}) {
		t.Fatalf("unexpected metadata = %#v", *metadata)
	}
	if !reflect.DeepEqual(template.Spec, corev1.NodeSpec{}) {
		t.Fatalf("unexpected spec = %#v", template.Spec)
	}
	if !reflect.DeepEqual(template.Status.Capacity, source.Status.Capacity) {
		t.Fatalf("capacity = %v, want %v", template.Status.Capacity, source.Status.Capacity)
	}
	if !reflect.DeepEqual(template.Status.Allocatable, source.Status.Allocatable) {
		t.Fatalf("allocatable = %v, want %v", template.Status.Allocatable, source.Status.Allocatable)
	}
	statusWithoutResources := template.Status.DeepCopy()
	statusWithoutResources.Capacity = nil
	statusWithoutResources.Allocatable = nil
	if !reflect.DeepEqual(*statusWithoutResources, corev1.NodeStatus{}) {
		t.Fatalf("unexpected status = %#v", *statusWithoutResources)
	}
}

func TestNodeGroupTemplateNodeInfoDoesNotAliasObservedState(t *testing.T) {
	source := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"kubernetes.io/os": "linux"}},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
			Allocatable: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("7Gi")},
		},
	}
	p := testProvider(t, false, nodeRunner(source))
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}
	observed := p.nodes[0].DeepCopy()

	first := templateNode(t, p)
	first.Labels["kubernetes.io/os"] = "mutated"
	first.Status.Capacity[corev1.ResourceCPU] = resource.MustParse("1")
	first.Status.Allocatable[corev1.ResourceMemory] = resource.MustParse("1Gi")

	second := templateNode(t, p)
	if second.Labels["kubernetes.io/os"] != "linux" {
		t.Fatalf("second labels = %v", second.Labels)
	}
	assertResources(t, second.Status.Capacity, "4", "0")
	if got := second.Status.Allocatable.Memory().String(); got != "7Gi" {
		t.Fatalf("allocatable memory = %s, want 7Gi", got)
	}
	if !reflect.DeepEqual(p.nodes[0], *observed) {
		t.Fatalf("observed node mutated: got %#v, want %#v", p.nodes[0], *observed)
	}
}

func TestNodeGroupTemplateNodeInfoValidatesState(t *testing.T) {
	p := testProvider(t, false, nodeRunner())
	tests := []struct {
		name   string
		id     string
		code   codes.Code
		phrase string
	}{
		{name: "unknown group", id: "unknown", code: codes.NotFound, phrase: "unknown node group"},
		{name: "no observed node", id: "minikube-workers", code: codes.FailedPrecondition, phrase: "no observed node"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.NodeGroupTemplateNodeInfo(context.Background(), &protos.NodeGroupTemplateNodeInfoRequest{Id: tt.id})
			if status.Code(err) != tt.code || !strings.Contains(err.Error(), tt.phrase) {
				t.Fatalf("error = %v, want %v containing %q", err, tt.code, tt.phrase)
			}
		})
	}
}

func TestScaleDownBoundary(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		id      string
		code    codes.Code
		phrase  string
		call    func(*Provider, string) error
	}{
		{name: "delete disabled", code: codes.FailedPrecondition, phrase: "scale-down disabled in this demo", id: "minikube-workers", call: deleteNodes},
		{name: "delete enabled", enabled: true, code: codes.Unimplemented, phrase: "scale-down is not implemented in this demo", id: "minikube-workers", call: deleteNodes},
		{name: "delete unknown group", enabled: true, code: codes.NotFound, phrase: "unknown node group", id: "unknown", call: deleteNodes},
		{name: "decrease disabled", code: codes.FailedPrecondition, phrase: "scale-down disabled in this demo", id: "minikube-workers", call: decreaseTarget},
		{name: "decrease enabled", enabled: true, code: codes.Unimplemented, phrase: "scale-down is not implemented in this demo", id: "minikube-workers", call: decreaseTarget},
		{name: "decrease unknown group", enabled: true, code: codes.NotFound, phrase: "unknown node group", id: "unknown", call: decreaseTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			var commands int
			p := testProviderWithLogger(t, false, func(context.Context, string, ...string) ([]byte, []byte, error) {
				commands++
				return nil, nil, errors.New("unexpected command")
			}, log.New(&logs, "", 0))
			p.cfg.EnableScaleDown = tt.enabled
			p.target = 2

			err := tt.call(p, tt.id)
			if status.Code(err) != tt.code || !strings.Contains(err.Error(), tt.phrase) {
				t.Fatalf("error = %v, want %v containing %q", err, tt.code, tt.phrase)
			}
			if p.target != 2 {
				t.Fatalf("target = %d, want 2", p.target)
			}
			if commands != 0 {
				t.Fatalf("commands = %d, want 0", commands)
			}
			if !strings.Contains(logs.String(), "scale-down rejected") {
				t.Fatalf("logs = %q", logs.String())
			}
		})
	}
}

func TestMinimalMiscRPCs(t *testing.T) {
	p := testProvider(t, false, nodeRunner())
	gpuLabel, err := p.GPULabel(context.Background(), &protos.GPULabelRequest{})
	if err != nil || gpuLabel == nil || gpuLabel.Label != "" {
		t.Fatalf("GPULabel() = %#v, %v", gpuLabel, err)
	}
	gpuTypes, err := p.GetAvailableGPUTypes(context.Background(), &protos.GetAvailableGPUTypesRequest{})
	if err != nil || gpuTypes == nil || len(gpuTypes.GpuTypes) != 0 {
		t.Fatalf("GetAvailableGPUTypes() = %#v, %v", gpuTypes, err)
	}
	cleanup, err := p.Cleanup(context.Background(), &protos.CleanupRequest{})
	if err != nil || cleanup == nil {
		t.Fatalf("Cleanup() = %#v, %v", cleanup, err)
	}
}

func TestOptionalRPCsRemainUnimplemented(t *testing.T) {
	p := testProvider(t, false, nodeRunner())
	tests := []struct {
		name string
		call func() error
	}{
		{name: "node price", call: func() error {
			_, err := p.PricingNodePrice(context.Background(), &protos.PricingNodePriceRequest{})
			return err
		}},
		{name: "pod price", call: func() error {
			_, err := p.PricingPodPrice(context.Background(), &protos.PricingPodPriceRequest{})
			return err
		}},
		{name: "node group options", call: func() error {
			_, err := p.NodeGroupGetOptions(context.Background(), &protos.NodeGroupAutoscalingOptionsRequest{})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call()); got != codes.Unimplemented {
				t.Fatalf("status = %v, want %v", got, codes.Unimplemented)
			}
		})
	}
}

func TestUnknownNodeGroupReturnsNotFound(t *testing.T) {
	p := testProvider(t, false, nodeRunner())
	tests := []struct {
		name string
		call func() error
	}{
		{name: "target", call: func() error {
			_, err := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "unknown"})
			return err
		}},
		{name: "nodes", call: func() error {
			_, err := p.NodeGroupNodes(context.Background(), &protos.NodeGroupNodesRequest{Id: "unknown"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call()); got != codes.NotFound {
				t.Fatalf("status = %v, want %v", got, codes.NotFound)
			}
		})
	}
}

func TestDryRunRefreshDoesNotDecreaseTarget(t *testing.T) {
	p := testProvider(t, true, nodeRunner(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}))
	p.target = 3
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}
	size, err := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if err != nil {
		t.Fatal(err)
	}
	if size.TargetSize != 3 {
		t.Fatalf("target size = %d, want 3", size.TargetSize)
	}
}

func TestRefreshPropagatesCommandFailure(t *testing.T) {
	cause := errors.New("exit 1")
	p := testProvider(t, false, func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("cluster unavailable"), cause
	})

	_, err := p.Refresh(context.Background(), &protos.RefreshRequest{})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "kubectl") || !strings.Contains(err.Error(), "cluster unavailable") {
		t.Fatalf("Refresh() error = %v", err)
	}
}

func TestNodeGroupIncreaseSizeDryRun(t *testing.T) {
	var logs bytes.Buffer
	var commands int
	run := func(context.Context, string, ...string) ([]byte, []byte, error) {
		commands++
		return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}), nil, nil
	}
	p := testProviderWithLogger(t, true, run, log.New(&logs, "", 0))
	p.target = 1

	if _, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1}); err != nil {
		t.Fatal(err)
	}
	if commands != 0 {
		t.Fatalf("commands after increase = %d, want 0", commands)
	}
	if got := targetSize(t, p); got != 2 {
		t.Fatalf("target size = %d, want 2", got)
	}
	if logText := logs.String(); !strings.Contains(logText, "group=minikube-workers") || !strings.Contains(logText, "delta=1") || !strings.Contains(logText, "current=1") || !strings.Contains(logText, "dry-run=true") || !strings.Contains(logText, "scale-up succeeded") {
		t.Fatalf("logs = %q", logText)
	}

	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := targetSize(t, p); got != 2 {
		t.Fatalf("target size after refresh = %d, want 2", got)
	}
}

func TestNodeGroupIncreaseSizeRejectsInvalidRequests(t *testing.T) {
	var commands int
	p := testProvider(t, true, func(context.Context, string, ...string) ([]byte, []byte, error) {
		commands++
		return nil, nil, errors.New("unexpected command")
	})
	p.target = 1

	tests := []struct {
		name string
		req  *protos.NodeGroupIncreaseSizeRequest
		code codes.Code
	}{
		{name: "beyond maximum", req: &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 3}, code: codes.FailedPrecondition},
		{name: "zero delta", req: &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers"}, code: codes.InvalidArgument},
		{name: "negative delta", req: &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: -1}, code: codes.InvalidArgument},
		{name: "unknown group", req: &protos.NodeGroupIncreaseSizeRequest{Id: "unknown", Delta: 1}, code: codes.NotFound},
		{name: "nil request", code: codes.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.NodeGroupIncreaseSize(context.Background(), tt.req)
			if got := status.Code(err); got != tt.code {
				t.Fatalf("status = %v, want %v (error %v)", got, tt.code, err)
			}
		})
	}
	if got := targetSize(t, p); got != 1 {
		t.Fatalf("target size = %d, want 1", got)
	}
	if commands != 0 {
		t.Fatalf("commands = %d, want 0", commands)
	}
}

func TestNodeGroupIncreaseSizeChecksObservedSizeBeforeAdding(t *testing.T) {
	var adds int
	p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if name == "minikube" {
			adds++
			return nil, nil, nil
		}
		return nodeList(
			corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}},
			corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		), nil, nil
	})
	p.nodes = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}}
	p.target = 1

	_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("status = %v, want %v (error %v)", status.Code(err), codes.FailedPrecondition, err)
	}
	if adds != 0 {
		t.Fatalf("adds = %d, want 0", adds)
	}
	if got := targetSize(t, p); got != 3 {
		t.Fatalf("target size = %d, want observed 3", got)
	}
}

func TestNodeGroupIncreaseSizeAddsAndRefreshesEachNode(t *testing.T) {
	commands := []string{"kubectl", "minikube", "kubectl", "minikube", "kubectl"}
	nodeCounts := []int{1, 2, 3}
	call := 0
	p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if call >= len(commands) || name != commands[call] {
			t.Fatalf("command %d = %q, want sequence %v", call, name, commands)
		}
		call++
		if name == "minikube" {
			return nil, nil, nil
		}
		count := nodeCounts[0]
		nodeCounts = nodeCounts[1:]
		nodes := make([]corev1.Node, count)
		for i := range nodes {
			nodes[i].Name = fmt.Sprintf("node-%d", i)
		}
		return nodeList(nodes...), nil, nil
	})
	p.nodes = []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}}
	p.target = 1

	if _, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 2}); err != nil {
		t.Fatal(err)
	}
	if call != len(commands) {
		t.Fatalf("commands run = %d, want %d", call, len(commands))
	}
	if got := targetSize(t, p); got != 3 {
		t.Fatalf("target size = %d, want 3", got)
	}
	if len(p.nodes) != 3 {
		t.Fatalf("observed nodes = %d, want 3", len(p.nodes))
	}
}

func TestNodeGroupIncreaseSizeMapsCommandFailures(t *testing.T) {
	t.Run("add node preserves refreshed partial progress", func(t *testing.T) {
		var logs bytes.Buffer
		call := 0
		run := func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
			call++
			switch call {
			case 1:
				if name != "kubectl" {
					t.Fatalf("command 1 = %q, want kubectl", name)
				}
				return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}), nil, nil
			case 2:
				if name != "minikube" {
					t.Fatalf("command 2 = %q, want minikube", name)
				}
				return nil, nil, nil
			case 3:
				if name != "kubectl" {
					t.Fatalf("command 3 = %q, want kubectl", name)
				}
				return nodeList(
					corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}},
					corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				), nil, nil
			case 4:
				if name != "minikube" {
					t.Fatalf("command 4 = %q, want minikube", name)
				}
				return nil, []byte("capacity exhausted"), errors.New("add failed")
			default:
				t.Fatalf("unexpected command %d: %s", call, name)
				return nil, nil, nil
			}
		}
		p := testProviderWithLogger(t, false, run, log.New(&logs, "", 0))
		p.target = 1

		_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 2})
		if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "add failed") || !strings.Contains(err.Error(), "capacity exhausted") {
			t.Fatalf("error = %v", err)
		}
		if call != 4 {
			t.Fatalf("commands = %d, want 4", call)
		}
		if got := targetSize(t, p); got != 2 {
			t.Fatalf("target size = %d, want 2", got)
		}
		if logText := logs.String(); !strings.Contains(logText, "scale-up failed") || !strings.Contains(logText, "progress=1/2") || !strings.Contains(logText, "current=2") {
			t.Fatalf("logs = %q", logText)
		}
	})

	t.Run("refresh nodes", func(t *testing.T) {
		call := 0
		p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
			call++
			if call == 1 && name == "kubectl" {
				return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}), nil, nil
			}
			if call == 2 && name == "minikube" {
				return nil, nil, nil
			}
			if call == 3 && name == "kubectl" {
				return nil, []byte("api unavailable"), errors.New("refresh failed")
			}
			t.Fatalf("unexpected command %d: %s", call, name)
			return nil, nil, nil
		})
		p.target = 1

		_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 2})
		if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "refresh failed") || !strings.Contains(err.Error(), "api unavailable") {
			t.Fatalf("error = %v", err)
		}
		if call != 3 {
			t.Fatalf("commands = %d, want 3", call)
		}
		if got := targetSize(t, p); got != 1 {
			t.Fatalf("target size = %d, want 1", got)
		}
	})
}

func TestRefreshAndIncreasePublishInOrder(t *testing.T) {
	staleStarted := make(chan struct{})
	releaseStale := make(chan struct{})
	addStarted := make(chan struct{}, 1)
	var kubectlCalls atomic.Int32
	var adds atomic.Int32
	p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if name == "minikube" {
			adds.Add(1)
			select {
			case addStarted <- struct{}{}:
			default:
			}
			return nil, nil, nil
		}
		if kubectlCalls.Add(1) == 1 {
			close(staleStarted)
			<-releaseStale
			return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}), nil, nil
		}
		nodes := make([]corev1.Node, 1+int(adds.Load()))
		return nodeList(nodes...), nil, nil
	})
	p.target = 1

	refreshDone := make(chan error, 1)
	go func() {
		_, err := p.Refresh(context.Background(), &protos.RefreshRequest{})
		refreshDone <- err
	}()
	<-staleStarted
	increaseDone := make(chan error, 1)
	increaseCtx := newObservedContext(context.Background())
	go func() {
		_, err := p.NodeGroupIncreaseSize(increaseCtx, &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		increaseDone <- err
	}()
	<-increaseCtx.observed
	if increaseCtx.client.Load() {
		<-addStarted
		if err := <-increaseDone; err != nil {
			t.Fatal(err)
		}
		close(releaseStale)
	} else {
		close(releaseStale)
		if err := <-increaseDone; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if got := targetSize(t, p); got != 2 {
		t.Fatalf("target size = %d, want 2", got)
	}
	if got := adds.Load(); got != 1 {
		t.Fatalf("adds = %d, want 1", got)
	}
}

func TestNodeGroupIncreaseSizeCanceledWhileWaiting(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	var adds atomic.Int32
	p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		if name == "minikube" {
			adds.Add(1)
			return nil, nil, nil
		}
		nodes := make([]corev1.Node, 1+int(adds.Load()))
		return nodeList(nodes...), nil, nil
	})
	p.target = 1

	firstDone := make(chan error, 1)
	go func() {
		_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		firstDone <- err
	}()
	<-firstStarted
	ctx, cancel := context.WithCancel(context.Background())
	waitingCtx := newObservedContext(ctx)
	secondDone := make(chan error, 1)
	go func() {
		_, err := p.NodeGroupIncreaseSize(waitingCtx, &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		secondDone <- err
	}()
	<-waitingCtx.observed
	cancel()
	secondErr := <-secondDone
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if got := status.Code(secondErr); got != codes.Canceled {
		t.Fatalf("status = %v, want %v (error %v)", got, codes.Canceled, secondErr)
	}
	if got := adds.Load(); got != 1 {
		t.Fatalf("adds = %d, want 1", got)
	}
	if got := targetSize(t, p); got != 2 {
		t.Fatalf("target size = %d, want 2", got)
	}
}

func TestNodeGroupIncreaseSizeSerializesRealAdds(t *testing.T) {
	firstAddStarted := make(chan struct{})
	secondAddStarted := make(chan struct{})
	releaseFirstAdd := make(chan struct{})
	var adds atomic.Int32
	p := testProvider(t, false, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if name == "minikube" {
			switch adds.Add(1) {
			case 1:
				close(firstAddStarted)
				<-releaseFirstAdd
			case 2:
				close(secondAddStarted)
			}
			return nil, nil, nil
		}
		nodes := make([]corev1.Node, 1+int(adds.Load()))
		return nodeList(nodes...), nil, nil
	})
	p.target = 1

	firstDone := make(chan error, 1)
	go func() {
		_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		firstDone <- err
	}()
	<-firstAddStarted
	secondDone := make(chan error, 1)
	secondCtx := newObservedContext(context.Background())
	go func() {
		_, err := p.NodeGroupIncreaseSize(secondCtx, &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		secondDone <- err
	}()
	<-secondCtx.observed
	select {
	case <-secondAddStarted:
		t.Fatal("second add started before first add was released")
	default:
	}
	close(releaseFirstAdd)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := targetSize(t, p); got != 3 {
		t.Fatalf("target size = %d, want 3", got)
	}
}

func TestNodeGroupIncreaseSizeMapsCallerCancellation(t *testing.T) {
	t.Run("add node canceled", func(t *testing.T) {
		addStarted := make(chan struct{})
		p := testProvider(t, false, func(ctx context.Context, name string, _ ...string) ([]byte, []byte, error) {
			if name == "kubectl" {
				return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}), nil, nil
			}
			close(addStarted)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
		p.target = 1
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := p.NodeGroupIncreaseSize(ctx, &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
			done <- err
		}()
		<-addStarted
		cancel()
		if err := <-done; status.Code(err) != codes.Canceled {
			t.Fatalf("status = %v, want %v (error %v)", status.Code(err), codes.Canceled, err)
		}
	})

	t.Run("node refresh deadline", func(t *testing.T) {
		var kubectlCalls int
		p := testProvider(t, false, func(ctx context.Context, name string, _ ...string) ([]byte, []byte, error) {
			if name == "minikube" {
				return nil, nil, nil
			}
			kubectlCalls++
			if kubectlCalls == 1 {
				return nodeList(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-0"}}), nil, nil
			}
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
		p.target = 1
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := p.NodeGroupIncreaseSize(ctx, &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("status = %v, want %v (error %v)", status.Code(err), codes.DeadlineExceeded, err)
		}
	})
}

func templateNode(t *testing.T, p *Provider) *corev1.Node {
	t.Helper()
	response, err := p.NodeGroupTemplateNodeInfo(context.Background(), &protos.NodeGroupTemplateNodeInfoRequest{Id: "minikube-workers"})
	if err != nil {
		t.Fatal(err)
	}
	var node corev1.Node
	if err := node.Unmarshal(response.NodeBytes); err != nil {
		t.Fatalf("unmarshal template: %v", err)
	}
	return &node
}

func assertResources(t *testing.T, resources corev1.ResourceList, cpu, memory string) {
	t.Helper()
	if got := resources.Cpu().String(); got != cpu {
		t.Fatalf("cpu = %s, want %s", got, cpu)
	}
	if got := resources.Memory().String(); got != memory {
		t.Fatalf("memory = %s, want %s", got, memory)
	}
}

func deleteNodes(p *Provider, id string) error {
	_, err := p.NodeGroupDeleteNodes(context.Background(), &protos.NodeGroupDeleteNodesRequest{Id: id, Nodes: []*protos.ExternalGrpcNode{{Name: "node"}}})
	return err
}

func decreaseTarget(p *Provider, id string) error {
	_, err := p.NodeGroupDecreaseTargetSize(context.Background(), &protos.NodeGroupDecreaseTargetSizeRequest{Id: id, Delta: -1})
	return err
}

func testProvider(t *testing.T, dryRun bool, run minikube.RunFunc) *Provider {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	return testProviderWithLogger(t, dryRun, run, logger)
}

func testProviderWithLogger(t *testing.T, dryRun bool, run minikube.RunFunc, logger *log.Logger) *Provider {
	t.Helper()
	p, err := New(
		Config{NodeGroup: "minikube-workers", MinNodes: 1, MaxNodes: 3, DryRun: dryRun},
		minikube.New("demo", time.Second, logger, run),
		logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func targetSize(t *testing.T, p *Provider) int32 {
	t.Helper()
	size, err := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if err != nil {
		t.Fatal(err)
	}
	return size.TargetSize
}

type observedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
	client   atomic.Bool
}

func newObservedContext(ctx context.Context) *observedContext {
	return &observedContext{Context: ctx, observed: make(chan struct{})}
}

func (c *observedContext) Deadline() (time.Time, bool) {
	c.client.Store(true)
	return c.Context.Deadline()
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func nodeRunner(nodes ...corev1.Node) minikube.RunFunc {
	return func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nodeList(nodes...), nil, nil
	}
}

func nodeList(nodes ...corev1.Node) []byte {
	b, err := json.Marshal(corev1.NodeList{Items: nodes})
	if err != nil {
		panic(err)
	}
	return b
}
