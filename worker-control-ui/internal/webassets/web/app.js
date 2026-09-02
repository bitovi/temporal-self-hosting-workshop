const state = {
  cluster: "cluster-1",
  logSources: { worker: null, runner: null },
};

const clusterBtns = document.querySelectorAll(".cluster-btn");

const panels = {
  worker: {
    toggle: document.getElementById("worker-toggle"),
    pill: document.getElementById("worker-pill"),
    log: document.getElementById("worker-log"),
    kill: document.getElementById("worker-kill"),
    toggleBusy: false,
  },
  runner: {
    toggle: document.getElementById("runner-toggle"),
    pill: document.getElementById("runner-pill"),
    log: document.getElementById("runner-log"),
    kill: document.getElementById("runner-kill"),
    toggleBusy: false,
  },
};

const runnerConfigEls = {
  modeSteady: document.getElementById("runner-mode-steady"),
  modeRate: document.getElementById("runner-mode-rate"),
  steadyGroup: document.getElementById("runner-config-steady"),
  rateGroup: document.getElementById("runner-config-rate"),
  depth: document.getElementById("runner-depth"),
  example: document.getElementById("runner-example"),
  rate: document.getElementById("runner-rate"),
  scenario: document.getElementById("runner-scenario"),
  scenarioGroups: {
    hello_world: document.getElementById("rate-scenario-hello-world"),
    many_timers: document.getElementById("rate-scenario-many-timers"),
    fan_out: document.getElementById("rate-scenario-fan-out"),
  },
  rateExample: document.getElementById("runner-rate-example"),
  concurrentTimers: document.getElementById("runner-concurrent-timers"),
  timerDuration: document.getElementById("runner-timer-duration"),
  children: document.getElementById("runner-children"),
  activities: document.getElementById("runner-activities"),
};

async function postJSON(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

async function getJSON(path) {
  const res = await fetch(path);
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function setPill(el, value) {
  el.textContent = value;
  el.className = "pill " + value;
}

function clearLog(el) {
  el.textContent = "";
}

function openLogStream(kind, targetEl) {
  closeLogStream(kind);
  const source = new EventSource(`/api/logs/${kind}/${state.cluster}`);
  source.onmessage = (event) => {
    targetEl.textContent += event.data + "\n";
    targetEl.scrollTop = targetEl.scrollHeight;
  };
  state.logSources[kind] = source;
}

function closeLogStream(kind) {
  if (state.logSources[kind]) {
    state.logSources[kind].close();
    state.logSources[kind] = null;
  }
}

function runnerConfig() {
  const mode = runnerConfigEls.modeRate.checked ? "rate" : "steady";
  if (mode === "steady") {
    return {
      mode,
      depth: parseInt(runnerConfigEls.depth.value, 10),
      example: parseInt(runnerConfigEls.example.value, 10),
    };
  }

  const scenario = runnerConfigEls.scenario.value;
  const cfg = { mode, scenario, rate: parseFloat(runnerConfigEls.rate.value) };
  if (scenario === "hello_world") {
    cfg.example = parseInt(runnerConfigEls.rateExample.value, 10);
  } else if (scenario === "many_timers") {
    cfg.concurrentTimers = parseInt(runnerConfigEls.concurrentTimers.value, 10) || 0;
    cfg.timerDurationSeconds = parseInt(runnerConfigEls.timerDuration.value, 10) || 0;
  } else if (scenario === "fan_out") {
    cfg.childrenPerWorkflow = parseInt(runnerConfigEls.children.value, 10) || 0;
    cfg.activitiesPerWorkflow = parseInt(runnerConfigEls.activities.value, 10) || 0;
  }
  return cfg;
}

// Tracked separately from the mode radios so refreshPanel (running state)
// and the mode radios (steady vs. rate) can both drive which knobs are
// touchable without stomping on each other.
let runnerRunning = false;

// Hides *and* disables whichever group doesn't apply to the current
// mode+scenario (e.g. depth/example do nothing in rate mode, concurrent-
// timers does nothing unless rate mode's scenario is "many_timers"), and
// disables everything while the runner is running (config only applies at
// Start). Both hidden and disabled matter here: hidden alone depends on CSS
// not re-asserting `display` on the element (a real bug hit here once
// already), so disabled is the belt-and-suspenders guarantee that inactive
// knobs are never actually touchable regardless of stylesheet changes.
function updateRunnerControls() {
  const rateMode = runnerConfigEls.modeRate.checked;
  const scenario = runnerConfigEls.scenario.value;

  runnerConfigEls.steadyGroup.hidden = rateMode;
  runnerConfigEls.rateGroup.hidden = !rateMode;
  Object.entries(runnerConfigEls.scenarioGroups).forEach(([name, el]) => {
    el.hidden = !(rateMode && scenario === name);
  });

  runnerConfigEls.modeSteady.disabled = runnerRunning;
  runnerConfigEls.modeRate.disabled = runnerRunning;
  runnerConfigEls.depth.disabled = runnerRunning || rateMode;
  runnerConfigEls.example.disabled = runnerRunning || rateMode;
  runnerConfigEls.rate.disabled = runnerRunning || !rateMode;
  runnerConfigEls.scenario.disabled = runnerRunning || !rateMode;
  runnerConfigEls.rateExample.disabled = runnerRunning || !(rateMode && scenario === "hello_world");
  runnerConfigEls.concurrentTimers.disabled = runnerRunning || !(rateMode && scenario === "many_timers");
  runnerConfigEls.timerDuration.disabled = runnerRunning || !(rateMode && scenario === "many_timers");
  runnerConfigEls.children.disabled = runnerRunning || !(rateMode && scenario === "fan_out");
  runnerConfigEls.activities.disabled = runnerRunning || !(rateMode && scenario === "fan_out");
}

runnerConfigEls.modeSteady.addEventListener("change", updateRunnerControls);
runnerConfigEls.modeRate.addEventListener("change", updateRunnerControls);
runnerConfigEls.scenario.addEventListener("change", updateRunnerControls);
updateRunnerControls();

clusterBtns.forEach((btn) =>
  btn.addEventListener("click", () => {
    if (btn.dataset.cluster === state.cluster) return;
    clusterBtns.forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    state.cluster = btn.dataset.cluster;
    Object.keys(panels).forEach((kind) => {
      closeLogStream(kind);
      clearLog(panels[kind].log);
    });
    refresh();
  })
);

Object.entries(panels).forEach(([kind, panel]) => {
  panel.toggle.addEventListener("change", async () => {
    panel.toggleBusy = true;
    try {
      if (panel.toggle.checked) {
        await postJSON(`/api/${kind}/${state.cluster}/start`, kind === "runner" ? runnerConfig() : undefined);
      } else {
        await postJSON(`/api/${kind}/${state.cluster}/stop`);
        closeLogStream(kind);
      }
    } catch (err) {
      console.error(err);
      panel.toggle.checked = !panel.toggle.checked;
    } finally {
      panel.toggleBusy = false;
    }
  });

  panel.kill.addEventListener("click", async () => {
    panel.kill.disabled = true;
    try {
      await postJSON(`/api/${kind}/${state.cluster}/kill`);
    } catch (err) {
      console.error(err);
    } finally {
      panel.kill.disabled = false;
    }
  });
});

async function refreshPanel(kind, panel) {
  try {
    const res = await getJSON(`/api/${kind}/${state.cluster}/status`);
    setPill(panel.pill, res.state);
    const running = res.state === "running" || res.state === "starting";
    if (!panel.toggleBusy) panel.toggle.checked = running;
    panel.kill.disabled = !running;
    if (kind === "runner") {
      runnerRunning = running;
      updateRunnerControls();
    }
    if (running && !state.logSources[kind]) {
      openLogStream(kind, panel.log);
    } else if (!running && state.logSources[kind]) {
      closeLogStream(kind);
    }
  } catch (err) {
    console.error(err);
  }
}

async function refresh() {
  await Promise.all(Object.entries(panels).map(([kind, panel]) => refreshPanel(kind, panel)));
}

// codec-worker isn't cluster-scoped like the panels above (codec-demo is a
// single fixed demo, always against cluster-1), so it's not part of the
// `panels` loop -- its toggle/poll never reads state.cluster.
let codecWorkerToggleBusy = false;
const codecWorkerToggle = document.getElementById("codec-worker-toggle");
const codecWorkerPill = document.getElementById("codec-worker-pill");

codecWorkerToggle.addEventListener("change", async () => {
  codecWorkerToggleBusy = true;
  try {
    await postJSON(`/api/codec-worker/${codecWorkerToggle.checked ? "start" : "stop"}`);
  } catch (err) {
    console.error(err);
    codecWorkerToggle.checked = !codecWorkerToggle.checked;
  } finally {
    codecWorkerToggleBusy = false;
  }
});

async function refreshCodecWorker() {
  try {
    const res = await getJSON("/api/codec-worker/status");
    setPill(codecWorkerPill, res.state);
    if (!codecWorkerToggleBusy) {
      codecWorkerToggle.checked = res.state === "running" || res.state === "starting";
    }
  } catch (err) {
    console.error(err);
  }
}

refresh();
refreshCodecWorker();
setInterval(refresh, 2000);
setInterval(refreshCodecWorker, 2000);
