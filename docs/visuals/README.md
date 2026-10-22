# Workshop visuals

Interactive, presenter-driven diagrams for concepts that used to be drawn live on Miro.
Each one is a single self-contained HTML file. Open it in a browser and present; no build step.

| Visual | Covers |
|---|---|
| [task-queue-separation.html](task-queue-separation.html) | Worker slots, long-running activities stalling fast work, scale-out vs. separate task queues, one workflow routing activities to two queues |
| [request-flow.html](request-flow.html) | How one workflow with one activity moves through Frontend, History, Matching and the database: 11 events, 7 writes, and replay after a worker crash |
| [codec-server.html](codec-server.html) | Data residency with a payload codec: plain text in the US database without one, encryption in the app and worker, and the codec server decoding for the Web UI and CLI (Exercise 8) |
| [history-shards.html](history-shards.html) | How workflows hash to shards, one write at a time per shard, the shards ÷ write-latency ceiling, why more History hosts don't raise it, and hot shards. Has a sandbox |
| [workflows-vs-activities.html](workflows-vs-activities.html) | What belongs in a workflow vs an activity: completed activities never re-run, unfinished ones retry from the start (so make them idempotent), and why workflow code must be deterministic |
| [zero-downtime-upgrade.html](zero-downtime-upgrade.html) | Upgrading with two replicated clusters: schema before server, one minor version at a time, namespace handover, and upgrading the other side (Exercise 7) |
| [failover-versioning.html](failover-versioning.html) | Multi-cluster replication: every event stamped with the namespace failover version, how failover bumps it (1 → 2 → 11), the async replication gap, and why the highest version wins a conflict |

## Presenting

- `→` / `Space` / `PgDn` next step, `←` / `PgUp` back, `1`–`9` jump (number of steps varies)
- `N` presenter notes, `P` pause, `R` replay step, `D` toggles half speed (on by default on the step-by-step pages), `F` fullscreen, `?` all keys
- Task queue and shards pages: `S` switches to **Sandbox**: sliders for arrival rate, share of slow activities, workers and slots, plus a "burst" button for live "what if…" questions

Deep links: `#step3` opens a step, `#sandbox` opens the sandbox, and `#ff=10` fast-forwards the simulation (useful for screenshots).
