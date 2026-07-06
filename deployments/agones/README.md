# Agones Deployment

Agones-managed deployment of the goAthena **zone** service. The gateway and
identity services run as standard Kubernetes Deployments (see
`deployments/kustomize/`) — only zone is stateful per game world and belongs on
Agones.

## Prerequisites

- Agones installed on the cluster (CRDs `agones.dev/v1` and `autoscaling.agones.dev/v1`)
- `kubectl` with cluster admin context for the target namespace
- Container image `ghcr.io/bouroo/goathena-zone:latest` reachable from cluster nodes
- Companion infrastructure deployed in the `goathena` namespace:
  - MariaDB (`mariadb.goathena.svc.cluster.local`)
  - Valkey (`valkey.goathena.svc.cluster.local`)
  - NATS (`nats.goathena.svc.cluster.local:4222`)

## Resources

| File | Resource |
|---|---|
| `gameserver-template.yaml` | `PersistentVolumeClaim` `zone-map-data` (5Gi, ReadOnlyMany) |
| `fleet.yaml` | Agones `Fleet` `zone-fleet` (4 replicas, Packed, Static port 7121) |
| `fleet-autoscaler.yaml` | Agones `FleetAutoscaler` (Buffer policy, size 3, 4 ≤ N ≤ 50, 30s sync) |

## Deploy

```bash
kubectl apply -k deployments/agones/
```

The namespace `goathena` is created by the kustomize base at
`deployments/kustomize/base/namespace.yaml`; deploy that first if the namespace
does not already exist:

```bash
kubectl apply -k deployments/kustomize/base/
```

## Verify

```bash
kubectl get fleet,gameServers,fleetautoscaler -n goathena
kubectl describe fleet zone-fleet -n goathena
kubectl logs -n goathena -l agones.dev/fleet=zone-fleet -c zone --tail=200
```

A healthy deployment reports:

- `zone-fleet` status `Ready`, with `replicas: 4` and `allocatedReplicas` matching expected
- `GameServer` instances in `Ready` state (allocatable via the Agones allocator)
- `FleetAutoscaler` `ScalingLimited` clear; `CurrentReplicas` between 4 and 50

## Override image / replicas per environment

Use a kustomize overlay alongside this base, e.g.:

```yaml
# deployments/agones/overlays/staging/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: goathena
resources:
  - ../../agones
images:
  - name: ghcr.io/bouroo/goathena-zone
    newName: ghcr.io/bouroo/goathena-zone
    newTag: 5.1.0-rc1
patches:
  - target: { kind: Fleet, name: zone-fleet }
    patch: |-
      - op: replace
        path: /spec/replicas
        value: 2
```
