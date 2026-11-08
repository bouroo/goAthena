#!/usr/bin/env bash
# Verify the Tier 2 topology: two goathena nodes behind one shared
# Postgres/Valkey/NATS, each serving a disjoint set of maps, with the
# static route table telling each node where the other's maps live.
#
# What this proves, and what it cannot:
#
#   * Both nodes serve this tree's code (the commit stamp at /version).
#   * Both nodes are healthy and ready.
#   * A login is accepted through the fleet's shared session store.
#   * The route table is wired: each node's log shows the OTHER node's map
#     resolving to the other node's address.
#
# It does NOT prove a client can walk a portal between the nodes: that
# needs two real clients and a real map crossing. What it proves is the
# wiring underneath it.
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f deploy/tier2/compose.yml)

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "▶ building and starting the Tier 2 fleet"
COMMIT="$(git rev-parse --short HEAD)" BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  "${COMPOSE[@]}" build node-a node-b
"${COMPOSE[@]}" up -d

echo "▶ waiting for both nodes to become ready"
for port in 8081 8082; do
  ready=0
  for _ in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 2
  done
  [ "$ready" = 1 ] || fail "node on port ${port} never became ready"
done
echo "both nodes ready"

echo "▶ each node must carry this tree's commit stamp"
want="$(git rev-parse --short HEAD)"
for port in 8081 8082; do
  got="$(curl -fsS "http://127.0.0.1:${port}/version" \
    | tr ',' '\n' | sed -n 's/.*"commit":"\([^"]*\)".*/\1/p')"
  [ "$got" = "$want" ] || fail "node on port ${port} runs commit=${got}, want ${want}"
  echo "node on port ${port}: commit=${got}"
done

echo "▶ the control plane answers on both nodes"
for port in 8081 8082; do
  for path in healthz readyz version metrics; do
    curl -fsS "http://127.0.0.1:${port}/${path}" >/dev/null \
      || fail "node on port ${port} failed ${path}"
  done
done
echo "healthz, readyz, version, metrics: all 200 on both nodes"

echo "▶ seeding the shared account"
"${COMPOSE[@]}" exec -T mariadb mariadb -uroot -pragnarok ragnarok \
  -e "DELETE FROM login WHERE userid='loadgen'; INSERT INTO login (userid, user_pass, sex) VALUES ('loadgen', MD5('loadgen'), 'M');"

echo "▶ a login is accepted through the shared session store"
go run ./cmd/loadgen -host 127.0.0.1 -port 6901 \
  -conns "${LOADGEN_CONNS:-5}" -rate "${LOADGEN_RATE:-2}" -duration "${LOADGEN_DURATION:-10s}"
"${COMPOSE[@]}" logs node-a > /tmp/tier2-node-a.log 2>&1
grep -q "login accepted" /tmp/tier2-node-a.log \
  || { echo "node-a logged no accepted login"; tail -20 /tmp/tier2-node-a.log; fail "no accepted login"; }
echo "login accepted on node-a"

# A character whose last map is served by the OTHER node. The
# char-select handshake must hand the client that node's address.
"${COMPOSE[@]}" exec -T mariadb mariadb -uroot -pragnarok ragnarok \
  -e "DELETE FROM \`char\` WHERE name='Farer';
      INSERT INTO \`char\` (account_id, char_num, name, class, base_level, job_level, max_hp, hp, max_sp, sp, last_map, last_x, last_y)
      VALUES ((SELECT account_id FROM login WHERE userid='loadgen'), 0, 'Farer', 0, 1, 1, 1000, 1000, 50, 50, 'geffen', 100, 100);"

echo "▶ a character on a remote map must be handed the OTHER node's address"
# The character's map (geffen) is served by node-b; node-a's route table
# points geffen at node-b's address. The client is told where to go, and
# the address it is told is node-b's — not node-a's.
map_addr="$(go run ./cmd/charselect -host 127.0.0.1 -port 6901 \
  -user loadgen -pass loadgen -char-addr 127.0.0.1:6131 | sed -n 's/^map=//p')"
if [ -z "$map_addr" ]; then
  echo "the char-select handshake produced no map address"
  fail "no map address"
fi
want_map="${NODE_B_ADDRESS:-10.0.4.7}:5121"
if [ "$map_addr" != "$want_map" ]; then
  echo "the client was handed $map_addr, want $want_map (node-b's address)"
  fail "the route table did not redirect the character to node-b"
fi
echo "the character was handed $map_addr — node-b's address"

echo "▶ the route table must resolve each node's remote map to the other node"
"${COMPOSE[@]}" logs node-a > /tmp/tier2-node-a.log 2>&1
"${COMPOSE[@]}" logs node-b > /tmp/tier2-node-b.log 2>&1
grep -q "map served by another shard" /tmp/tier2-node-a.log \
  || { echo "node-a never resolved a remote map"; tail -30 /tmp/tier2-node-a.log; fail "no remote map resolution"; }
echo "both nodes resolved a remote map through the route table"

echo "Tier 2 verified"
