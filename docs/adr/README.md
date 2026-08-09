# Architecture Decision Records

This directory records the durable *why* behind goAthena's load-bearing
decisions. Each ADR captures the context, decision, and consequences of a
choice that shapes the codebase. It is the counterpart to `docs/roadmap.md`,
which records *status* (what is done and what is next); an ADR records *why*
the decision was made and is not updated as status changes.

## Index

| ADR | Title | Status |
|---|---|---|
| [0001](0001-single-packetver-thai-classic.md) | Target a single PACKETVER (Thai Classic 20250604), not multi-client | Accepted |
| [0002](0002-conn-context-auth-on-eventloop.md) | Cache and resolve per-connection auth on the gnet eventloop | Accepted |
| [0003](0003-data-file-fault-tolerance.md) | Tolerate (don't abort on) rAthena data-file quirks at load time | Accepted |

## Format

Each ADR follows the standard sections: Status, Context, Decision,
Consequences, and (where useful) References. Citations point to code locations
(`file:line`) and the git commit that established the decision (`HEAD` at
time of writing: `d257fd5`).
