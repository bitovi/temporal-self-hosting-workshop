# Workshop visuals

Interactive, presenter-driven diagrams for concepts that used to be drawn live on Miro.
Each one is a single self-contained HTML file. Open it in a browser and present; no build step.

| Visual | Covers |
|---|---|
| [task-queue-separation.html](task-queue-separation.html) | Worker slots, long-running activities stalling fast work, scale-out vs. separate task queues, one workflow routing activities to two queues |
| [request-flow.html](request-flow.html) | How one workflow with one activity moves through Frontend, History, Matching and the database: 11 events, 7 writes, and replay after a worker crash |

## Presenting

- `→` / `Space` / `PgDn` next step, `←` / `PgUp` back, `1`–`9` jump
- `N` presenter notes, `P` pause, `R` replay step, `D` half speed (request flow), `F` fullscreen, `?` all keys
- Task queue page: `S` switches to **Sandbox**: sliders for arrival rate, share of slow activities, workers and slots, plus a "burst" button for live "what if…" questions

Deep links: `#step3` opens a step, `#sandbox` opens the sandbox, and `#ff=10` fast-forwards the simulation (useful for screenshots).
