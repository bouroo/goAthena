# Production notes

The prod overlay differs from the base in three ways, and all three
need a decision from the operator before it is applied.

## 1. The Secret

`base/secret.yaml` holds a placeholder password and says so in its own
comments. The base must apply as-is on a workstation or a CI cluster,
so it carries a value that is obviously not a secret.

Replace it before this overlay goes anywhere near players' characters:

```sh
kubectl -n goathena create secret generic goathena-db \
  --from-literal=password="$(openssl rand -base64 32)" \
  --from-literal=postgres-password="$(openssl rand -base64 32)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

In a cluster with a secret controller, use that instead — the
manifest stays out of the repository and the value never passes through
the operator's shell history:

- **External Secrets Operator** — syncs from Vault, AWS Secrets Manager,
  or GCP Secret Manager.
- **Sealed Secrets** — a sealed secret is encrypted against the
  cluster's public key, so it can live in git.

Both are a small addition to the cluster, and both are the right answer
for anything a player has touched.

## 2. The image tag

`patch-image-goathena-prontera.yaml` and `patch-image-goathena-geffen.yaml`
pin the shards to `0.1.0-beta.16`. Update both to the release you want
running.

**The image tag has no `v` prefix, even though the release tag does.**
The release is cut as `v0.1.0-beta.16`, but the publishing workflow sets
the image's tags with `type=semver`, and semver's `{{version}}` is
`0.1.0-beta.16` — the `v` belongs to the git tag, not the image. Pinning
`v0.1.0-beta.16` gives `ImagePullBackOff` on every shard.

The base uses `latest`, which is right for a dev cluster and wrong for
production: a rolling update pulls whatever was published last, so two
pods of the same Fleet could run different code.

## 3. The addresses the client is handed

`patch-address.yaml` sets `GATEWAY_CHAR_HOST` and `GATEWAY_MAP_HOST`.
Replace `203.0.113.10` — a documentation address — with the ingress's
external address.

The client is told the char and map endpoints inside `AC_ACCEPT_LOGIN`,
and that handshake carries an **IPv4 address**, not a hostname. The
shard resolves the configured host to an IPv4 and puts it on the wire.
The client then dials that address directly.

The base sets those hosts to the `goathena-login` Service's name, which
resolves to the ClusterIP: correct for a client inside the cluster,
useless for one outside it. The ClusterIP is not routable from the
internet, and a client on a home network cannot resolve the name at
all.

For a public deployment, the address must be the one a client can
actually reach:

- **LoadBalancer Service** — the cloud provider assigns a public IP.
  Point the hosts at that IP.
- **NodePort** — the node's IP on the port the Service exposes. Each of
  the three ports needs its own node IP and port pair, and the client is
  told only one address, so the other two must be reachable on the same
  address too — which means the ingress, not a bare NodePort.
- **An ingress in host-network mode** — the node's IP, and the ingress
  listens on the node itself.

A wrong address here fails in the worst way: the login succeeds, and
the client hangs when it tries to reach the char server. A refused
connection would be better.

## The database

`base/postgres.yaml` runs Postgres in-cluster with one replica and a
`ReadWriteOnce` volume. The prod overlay keeps it at one replica: a
second replica would mount the same volume, and two writers would
corrupt it.

This is a development database. Before it holds players' characters,
replace it with a managed one (RDS, Cloud SQL, or a self-hosted
operator with real replication) and point `DB_HOST` at it. The backup
and failover story matters more than the shard count does.
