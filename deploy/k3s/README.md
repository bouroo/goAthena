# goAthena on Kubernetes (Tier 3)

The third deployment tier: a Kubernetes cluster running
[Agones](https://agones.dev), where a shard is a GameServer and Agones
owns the fleet's size.

This is the tier for a dedicated machine. The compose stack (Tier 1) is
for a laptop; this one is for a box you can point a public address at.

## The pieces

```
deploy/k3s/
  base/                     the whole tier, one resource per file
    namespace.yaml          the goathena namespace
    secret.yaml             the database password (a placeholder — replace it)
    configmap.yaml          the file config
    service.yaml            the client-facing and control-plane Services
    postgres.yaml           the database, with its volume
    valkey.yaml             the session store
    nats.yaml               the bus
    otel-collector.yaml     the trace and metric collector
    zone-fleet.yaml         the shards: one Fleet per map set
    fleet-autoscaler.yaml   how a map set grows
    ingress.yaml            the client-facing ports
  overlays/
    dev/                    small resources, `latest` image
    prod/                   pinned image, real resources, real addresses
```

## Installing Agones

The Fleets are Agones custom resources, so Agones must be installed
first. It is a Helm chart:

```sh
helm repo add agones https://agones.dev/chart/stable
helm install agones agones/agones \
  --namespace agones --create-namespace \
  --set image.tag=v1.51.0 \
  --set feature.ping=false
```

Wait for the controller and the CRDs:

```sh
kubectl -n agones wait --for=condition=Ready pod \
  -l app=agones-controller --timeout=120s
kubectl get crd fleets.agones.dev fleetautoscalers.agones.dev
```

`feature.ping=false` turns off the Agones "ping" port (9900) on every
GameServer. The shard does not need it, and it is one fewer thing
listening on every node.

## Applying

```sh
kubectl apply -k deploy/k3s/overlays/dev
```

Watch the shards come up:

```sh
kubectl -n goathena get fleets
kubectl -n goathena get gameservers
kubectl -n goathena logs -l app.kubernetes.io/component=zone -f
```

The shard reports itself ready through the Agones sidecar: the app
streams `Health()` every 5 seconds, and Agones evicts a GameServer after
three missed periods.

## Before production

Three things must change, and the prod overlay carries placeholders for
all three.

**1. The Secret.** `base/secret.yaml` holds a placeholder password and
says so in its own comments. Replace it:

```sh
kubectl -n goathena create secret generic goathena-db \
  --from-literal=password="$(openssl rand -base64 32)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

**2. The image tag.** `overlays/prod/patch-image-goathena-*.yaml` pins
the shards to `0.1.0-beta.16`. Update it to the release you want
running. The base uses `latest`, which is right for a dev cluster and
wrong for production: two pods of the same Fleet could run different
code.

The image tag carries **no `v` prefix**, even though the release tag
does: the release is `v0.1.0-beta.16` and the image is
`0.1.0-beta.16`. The publishing workflow sets the image's tags with
`type=semver`, and semver's `{{version}}` has no `v`. Pinning the
release's own tag form gives `ImagePullBackOff` on every shard.

**3. The addresses the client is handed.** This is the one that bites.

The client is told the char and map endpoints inside `AC_ACCEPT_LOGIN`,
and that handshake carries an **IPv4 address**, not a hostname. The shard
resolves `gateway.char_host` to an IPv4 and puts it on the wire.

The base sets those to the `goathena-login` Service's name, which
resolves to the ClusterIP. That is correct for a client *inside* the
cluster and useless for one outside it: the ClusterIP is not routable
from the internet, and a client on a home network cannot even resolve
the name.

For a client outside the cluster, set `GATEWAY_CHAR_HOST` and
`GATEWAY_MAP_HOST` to the ingress's external address — the IP a
LoadBalancer Service was given, or the node IP a NodePort listens on.
`overlays/prod/patch-address.yaml` carries a placeholder (`203.0.113.10`,
a documentation address) for exactly this.

A wrong address here fails in the worst way: the login succeeds, and the
client hangs when it tries to reach the char server. Prefer a refused
connection.

## Scaling

Agones owns the shard count, not the Kubernetes HPA — an HPA cannot
scale a Fleet, and one that tried would fight Agones for the replicas.

Two `FleetAutoscaler` policies are in `base/fleet-autoscaler.yaml`:

- **Buffer** — scale on the number of READY GameServers waiting to be
  allocated. This is the policy that matches how a zone behaves: a busy
  map set accumulates unallocated shards, and Agones adds more until the
  buffer drains.
- **Schedule** — scale on a cron window, for the predictable load (an
  event's opening hours) where waiting for the buffer to fill would
  queue the first players behind a cold start.

A third policy is deliberately absent. A **List** policy scales on the
player count the shard reports, which would be the sharpest signal, but
the app does not publish a player count — there is no capacity endpoint
on the Agones adapter. An autoscaler reading a metric nobody exports
scales on nothing.

The schedule in the base is a placeholder (`0 0 * * *`, two hours
long). Set it to the world's hours, in UTC, before it runs in production.

To scale a map set by hand:

```sh
kubectl -n goathena patch fleet goathena-prontera \
  --type=merge -p '{"spec":{"replicas":5}}'
```

## Checking the manifests

Before applying, the manifests can be checked against the real API
schema — the core APIs and Agones' own CRDs:

```sh
./scripts/verify-k3s.sh dev
./scripts/verify-k3s.sh prod
```

The script builds the overlay with kustomize and validates every
resource with [kubeconform](https://github.com/yannh/kubeconform). The
Agones CRD schemas are extracted from the Agones release manifest with
`scripts/crdschema`, because the built-in schema catalog does not carry
Agones — and the Fleets are the resources most worth checking, since
they carry the shard's contract with the transit MapDirectory.

## Notes on the manifests

**The shard's ports.** The game port must be named `game` — the transit
adapter reads `Status.Ports["game"].Port` out of the Agones API. The
port policy is Static: the container binds 5121 at boot and nothing
tells it a dynamic host port. The cost is one GameServer per node,
since the port cannot be shared.

**The namespace.** Each resource carries its namespace explicitly rather
than through kustomize's `namespace:` transformer, because the base
declares the Namespace object itself and the transformer collides with
it.

**The backing services' pod security.** They carry a
`pod-security.kubernetes.io/enforce: baseline` label to escape the
namespace's `restricted` profile: the upstream images run as their own
users and need writable roots. The shards satisfy `restricted`
unlabelled.

**The database.** Postgres writes to a `ReadWriteOnce` volume, so it
stays at one replica — a second replica would mount the same volume and
two writers would corrupt it. A standby needs replication, which is more
than this tier does; a managed database is the honest production answer.
The base's backing services are for development, not for holding
players' characters.
