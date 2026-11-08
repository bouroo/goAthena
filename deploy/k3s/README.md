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
    single/                 the one-shard GameServer (an alternative, not
                            an addition — the overlay picks it over the
                            Fleets)
  overlays/
    dev/                    small resources, `latest` image
    prod/                   pinned image, real resources, real addresses
    single/                 one GameServer instead of the Fleets
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

The chart's `agones.featureGates` is left at its default, which turns
on `CountsAndLists` — the gate the player counter and the Counter policy
need. If the chart is installed with a custom gate string, that gate
must include `CountsAndLists=true`; without it the controller rejects
the Fleets' counter and the Counter policy.

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
the shards to `0.1.0-beta.19`. Update it to the release you want
running. The base uses `latest`, which is right for a dev cluster and
wrong for production: two pods of the same Fleet could run different
code.

The image tag carries **no `v` prefix**, even though the release tag
does: the release is `v0.1.0-beta.19` and the image is
`0.1.0-beta.19`. The publishing workflow sets the image's tags with
`type=semver`, and semver's `{{version}}` has no `v`. Pinning the
release's own tag form gives `ImagePullBackOff` on every shard.

The published image is **amd64 only** — the release workflow builds it on
`ubuntu-latest` with no `platforms` matrix, so the manifest index carries
one linux/amd64 entry. An arm64 node (an Apple Silicon workstation, an
Ampere Graviton node) cannot pull it: the pull fails with `no image
found in image index for architecture "arm64"`. On an arm64 cluster, the
shards need an arm64 image — either a multi-arch build of the release
workflow, or a locally built one pushed to a registry the cluster can
reach.

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

Three `FleetAutoscaler` policies are in `base/fleet-autoscaler.yaml`:

- **Counter** — scale on the number of players connected across the
  fleet. This is the sharp signal: each shard publishes its player count
  to the sidecar every 10 seconds, Agones aggregates the counts across
  the fleet, and the fleet is sized so the aggregate capacity covers
  the aggregate players. A busy world grows; a quiet one shrinks.
- **Buffer** — scale on the number of READY GameServers waiting to be
  allocated. This covers the load the counter cannot see: a shard that
  is up but not yet loaded still has to exist before its first player
  arrives, and a cold start takes longer than a report interval.
- **Schedule** — scale on a cron window, for the predictable load (an
  event's opening hours) where waiting for the counter to climb would
  queue the first players behind a cold start.

The counter's name is a contract between three places, and a typo in any
one of them leaves the autoscaler scaling on a counter that does not
exist:

1. the Fleet declares it — `spec.template.spec.counters.players`
2. the shard publishes it — `agones.PlayersCounterName` (`"players"`)
3. the policy reads it — `spec.policy.counter.key`

The manifest test asserts the three agree.

The counter's capacity (100) is the shard's design point: the players
one shard serves before another is needed. The policy's `minCapacity`
and `maxCapacity` are the fleet's — one shard's worth at the floor, ten
shards' worth at the ceiling.

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
adapter reads `Status.Ports["game"].Port` out of the Agones API.

The port policy is **Dynamic**: Agones picks an open host port, and the
shard advertises the one it was given. It reads the allocation from the
sidecar at boot (`GetGameServer`) and puts the node address and the
assigned port in the handshake it hands the client.

This matters because the address is runtime state. The pod's IP is
assigned after the manifest is written, so no environment variable can
express it — a shard that advertised `gateway.map_host` would tell the
client to dial an address that is not this shard's. It is also what lets
a node carry several shards: a Static policy pins the host port to the
container port, so only one shard fits per node.

**The single-shard overlay.** `overlays/single` deploys one
GameServer instead of the Fleets — a hand-allocated boss zone, a staging
shard, a smoke test. It is an alternative to the Fleets, not an addition:
the overlay removes the Fleets and their autoscalers, because an
autoscaler scales Fleets and a Fleet that is not deployed has nothing to
scale.

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
