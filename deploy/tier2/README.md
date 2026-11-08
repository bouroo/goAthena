# Tier 2 — Small fleet (self-hosted, a few nodes)

The middle rung of the ladder: more than one game process, no Kubernetes.

```
deploy/tier2/
  compose.yml    two goathena nodes over one shared Postgres/Valkey/NATS
```

## What it is

Two or more `goathena` processes, each serving a **disjoint set of maps**,
over one shared Postgres (durable state), one shared Valkey (sessions, so
any node can resume a login) and one shared NATS (the bus).

The nodes find each other through a **static route table**: each node's
configuration names the maps served by *another* node and that node's
address. A map with no route is served by this process — so the table is
the fleet minus one, and no node ever redirects a client to itself.

```sh
# node-a serves prontera, node-b serves geffen.
ZONE_DIRECTORY_MODE=static
ZONE_DIRECTORY_ROUTES=geffen=10.0.4.7:5121
```

The table reaches the client through the **per-map address handoff**: the
char server resolves the endpoint for the character's *own* map, so a
character standing on a map another node serves is handed that node's
address. The client dials whatever the handshake carries — a node that
always advertised its own address would put the character in the wrong
world.

## Running it

```sh
docker compose -f deploy/tier2/compose.yml up -d
```

The shipped file puts both nodes on one host, with a distinct host-port
block each. The node *addresses* default to `10.0.4.6` and `10.0.4.7` —
the addresses the route table and the handshake carry. Override them for
a real deployment:

```sh
NODE_A_ADDRESS=192.168.1.6 NODE_B_ADDRESS=192.168.1.7 \
  docker compose -f deploy/tier2/compose.yml up -d
```

The client dials an IPv4 literal, never a hostname, so the addresses must
be the ones the client can reach.

## Verifying it

```sh
task verify-tier2
```

The check proves:

- both nodes serve **this tree's code** (the commit stamp at `/version`);
- both nodes are healthy (`/healthz`, `/readyz`, `/version`, `/metrics`);
- a login is accepted through the shared session store;
- **a character on a remote map is handed the other node's address** — the
  route table's whole job.

The last one drives the real handshake: `cmd/charselect` logs in, enters
the char server and selects a character, then reads the map address out
of `HC_NOTIFY_ZONESVR`. The character's map is `geffen` (served by
node-b), so the address the client is handed must be node-b's.

It does **not** prove a client can walk a portal between the nodes: that
needs two real clients and a real map crossing. What it proves is the
wiring underneath it.

## What the shared services buy

- **Valkey** holds the login sessions, so a character that logs in
  through node-a can enter the world through node-b. A node with its own
  session store would refuse the second node's clients.
- **NATS** is the bus between the nodes — the same one the modular
  monolith's remote services use (`economy` over NATS is the shipped
  example).
- **Postgres** holds the durable state: characters, inventory, the
  economy ledger. The character row's `last_map` is what the route table
  lookup is keyed on.
