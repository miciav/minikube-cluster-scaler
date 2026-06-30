# Minikube Scale-Down Design

## Goal

Extend the demonstration-only externalgrpc provider so Kubernetes Cluster Autoscaler can automatically reduce a minikube profile from N nodes back to one node, deleting exactly one worker at a time and never deleting the control-plane node.

## Scope

The feature implements `NodeGroupDeleteNodes` for one existing worker per request. It keeps `NodeGroupDecreaseTargetSize` unimplemented because this provider creates minikube nodes synchronously and therefore has no requested-but-unprovisioned capacity to cancel.

Scale-down remains disabled unless the provider is started with `--enable-scale-down`. The supplied scripts enable it for the complete scenario.

## Provider Flow

Cluster Autoscaler selects an unneeded node and sends one `NodeGroupDeleteNodes` request. The provider serializes the request with the existing operation gate, refreshes the observed node list, and validates all of the following before executing any command:

- the requested group matches the configured group;
- scale-down is enabled;
- the request contains exactly one node;
- the observed size is greater than `min-nodes`;
- the requested node belongs to the observed group;
- the requested node is not labelled `node-role.kubernetes.io/control-plane`;
- the requested node has a usable Kubernetes node name.

The provider resolves the externalgrpc node by name or provider ID, then invokes:

```sh
minikube node delete <node-name> -p <profile>
```

After the command succeeds, it refreshes the node list. The RPC succeeds only after the deleted node is absent from the observed state. Operations and failures use the existing structured log style.

## Sequential Deletion

Cluster Autoscaler runs with `--max-scale-down-parallelism=1`. The provider also rejects requests containing anything other than one node. These two checks preserve the N to N-1 behavior even if configuration changes or a caller bypasses the supplied manifest.

The existing operation gate prevents scale-up, refresh, and scale-down from changing provider state concurrently.

## Dry Run

In dry-run mode, a valid delete request removes the selected worker only from the provider's simulated node list and decreases the simulated target by one. No minikube command runs. The control-plane and minimum-size checks remain active.

## Error Handling

Unknown groups and nodes return `NotFound`. Disabled scale-down, minimum-size violations, and control-plane deletion attempts return `FailedPrecondition`. Empty or multi-node requests return `InvalidArgument`. Minikube command failures and inconsistent post-delete state return `Internal`; caller cancellation and deadlines retain their gRPC context status.

The provider does not decrement real-mode state before Minikube confirms deletion. A failed command therefore leaves the last observed state intact, and the next refresh reconciles it with Kubernetes.

## Cluster Autoscaler Configuration

The supplied deployment enables scale-down and sets:

```text
--max-scale-down-parallelism=1
--scale-down-delay-after-add=$(SCALE_DOWN_DELAY_AFTER_ADD)
--scale-down-unneeded-time=$(SCALE_DOWN_UNNEEDED_TIME)
```

Both environment variables default to `1m` in the manifest. `scripts/03-deploy-cluster-autoscaler.sh` accepts host environment overrides with the same names and applies them to the Deployment. Cluster Autoscaler's scan interval remains at its upstream default.

## Scenario and Documentation

The pressure workload first demonstrates scale-up from one to two nodes. A new script removes the pressure workload, allowing Cluster Autoscaler to select and delete the added worker. The watch output and end-to-end verification confirm that node count transitions from 2 to 1 and that the remaining node has the control-plane label.

The README documents the implemented RPC, safety rules, configurable delays, the N to 1 behavior, dry-run behavior, and the continued non-production status.

## Verification

Automated checks cover:

- the exact minikube delete command;
- successful worker deletion;
- name and provider-ID resolution;
- disabled scale-down;
- unknown groups and nodes;
- empty and multi-node requests;
- minimum-size and control-plane protection;
- dry-run behavior;
- command failures, context cancellation, and post-delete reconciliation;
- manifest defaults and script overrides;
- the live 1 to 2 to 1 scenario.

