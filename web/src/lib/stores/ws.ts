/**
 * WebSocket store — reactive, auto-reconnecting, zero-config.
 *
 * Every message from the Go server is parsed and dispatched to Svelte stores.
 * The UI is optimistically responsive — local state updates BEFORE the server
 * confirms, so interactions feel instant (< 16ms perceived latency).
 */

import { writable, derived, get } from "svelte/store";
import { checkpointManifest, checkpointSpec } from './refine';

export type Phase =
  | "idle"
  | "ideation"
  | "researching"
  | "compiling_spec"
  | "spec_review"
  | "dag_review"
  | "generating"
  | "complete"
  | "refine"
  | "paused"
  | "error";

export interface ChatMessage {
  role: "user" | "assistant" | "error";
  content: string;
  timestamp: number;
}

export interface FileProgress {
  path: string;
  healed: boolean;
  failed: boolean;
  duration_ms: number;
}

export interface RunComplete {
  output_path: string;
  file_count: number;
  healed: number;
  failed: number;
  cost: number;
  budget_left: number;
  coverage: number;
  project_name: string;
}

export interface RunSummary {
  run_id: string;
  project_name: string;
  phase: string;
}

// ─── Stores ───
export const connected = writable(false);
export const reconnectAttempts = writable(0);
export const reconnectFailed = writable(false);
export const phase = writable<Phase>("idle");
export const messages = writable<ChatMessage[]>([]);
export const interviewDone = writable(false);
export const manifest = writable<object | null>(null);
export const specData = writable<object | null>(null);
export const specFileCount = writable(0);
export const specTestCount = writable(0);
export const dagSlices = writable<string[][]>([]);
export const dagEstSeconds = writable(0);
export const dagEstCost = writable<string>("");
export const showPreCompile = writable(false);
export const filesCompleted = writable<FileProgress[]>([]);
export const totalFiles = writable(0);
export const runResult = writable<RunComplete | null>(null);
export const budgetSpent = writable(0);
export const budgetRemaining = writable(0);
export const errorMsg = writable<string | null>(null);
export const elapsed = writable(0);
export const recentRuns = writable<RunSummary[]>([]);

/** Profile auto-selected from project description; updated via profile_chosen events. */
export const selectedProfile = writable<string>("fastapi-async");

/** Tracks the result of the runtime smoke test (null = not run or passed). */
export const runtimeValidationError = writable<{ message: string; logs: string } | null>(null);

/** Tracks spec compilation progress (pass number, status, message). */
export const specProgress = writable<{ pass: number; status: string; message: string } | null>(null);

// Derived
export const progress = derived(
  [filesCompleted, totalFiles],
  ([$files, $total]) => ($total > 0 ? $files.length / $total : 0),
);

// ─── WebSocket connection ───
let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let elapsedTimer: ReturnType<typeof setInterval> | null = null;
let phaseStart = 0;

function getWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export function connect() {
  if (ws && ws.readyState <= 1) return; // already open/opening

  ws = new WebSocket(getWsUrl());

  ws.onopen = () => {
    connected.set(true);
    reconnectAttempts.set(0);
    reconnectFailed.set(false);
    errorMsg.set(null);
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    syncServerState();
  };

  ws.onclose = () => {
    connected.set(false);
    scheduleReconnect();
  };

  ws.onerror = () => {
    connected.set(false);
  };

  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      dispatch(msg);
    } catch {
      // ignore malformed frames
    }
  };
}

let visibilityListenerAdded = false;

function scheduleReconnect() {
  if (reconnectTimer) return;
  
  const attempt = get(reconnectAttempts);
  
  // Hard cap at 5 attempts to save resources
  if (attempt >= 5) {
    reconnectFailed.set(true);
    errorMsg.set("Connection lost. Please refresh the page or restart the server.");
    return;
  }

  // Pause polling if the tab is hidden
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
    if (!visibilityListenerAdded) {
      document.addEventListener('visibilitychange', function onVisChange() {
        if (document.visibilityState === 'visible') {
          document.removeEventListener('visibilitychange', onVisChange);
          visibilityListenerAdded = false;
          connect();
        }
      });
      visibilityListenerAdded = true;
    }
    return;
  }

  // Exponential backoff: 1s, 2s, 4s, 8s, 16s, max 30s
  const delay = Math.min(1000 * Math.pow(2, attempt), 30000);
  
  reconnectAttempts.update((n) => n + 1);
  
  // Show warning after 3 attempts but keep retrying up to 5
  if (attempt >= 3) {
    reconnectFailed.set(true);
  }
  
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, delay);
}

function dispatch(msg: any) {
  switch (msg.type) {
    case "phase_changed": {
      phase.set(msg.to as Phase);
      phaseStart = Date.now();
      startElapsedTimer();
      break;
    }

    case "interview_response":
      messages.update((m) => [
        ...m,
        { role: "assistant", content: msg.content, timestamp: Date.now() },
      ]);
      if (msg.done) {
        interviewDone.set(true);
        manifest.set(msg.manifest || null);
        showPreCompile.set(true);
      }
      break;

    case "spec_ready":
      specData.set(msg.spec);
      specFileCount.set(msg.file_count);
      specTestCount.set(msg.test_count);
      totalFiles.set(msg.file_count);
      break;

    case "dag_ready": {
      dagSlices.set(msg.slices);
      dagEstSeconds.set(msg.est_seconds);
      // Estimate cost: if server sends est_cost use it, otherwise estimate
      if (msg.est_cost) {
        dagEstCost.set(`$${msg.est_cost.toFixed(4)}`);
      } else {
        const fileCount = msg.slices.flat().length;
        const low = ((fileCount * 2000 * 0.28) / 1_000_000).toFixed(3);
        const high = (((fileCount * 2000 * 0.28) / 1_000_000) * 2).toFixed(3);
        dagEstCost.set(`$${low} - $${high}`);
      }
      break;
    }

    case "file_completed":
      filesCompleted.update((f) => [
        ...f,
        {
          path: msg.path,
          healed: msg.healed,
          failed: msg.failed,
          duration_ms: msg.duration_ms,
        },
      ]);
      break;

    case "budget_updated":
      budgetSpent.set(msg.run_spent);
      budgetRemaining.set(msg.remaining);
      break;

    case "run_complete":
      runResult.set(msg as RunComplete);
      // Populate refinement stores from the actual checkpoint data
      if (msg.manifest) checkpointManifest.set(msg.manifest);
      if (msg.spec) checkpointSpec.set(msg.spec);
      phase.set("complete");
      stopElapsedTimer();
      loadRecentRuns();
      break;

    case "error":
      errorMsg.set(msg.message);
      if (msg.fatal) phase.set("error");
      break;

    case "warning":
      // Multi-tab warning or other non-fatal server warnings — show briefly
      console.warn("[Pragma]", msg.message);
      break;

    case "profile_chosen":
      selectedProfile.set(msg.profile);
      break;

    case "extend_project_ready":
      // Delta spec from extend_project — store for future use
      console.log("[Pragma] extend_project_ready for run", msg.run_id);
      break;

    case "log":
      // Could pipe to a debug panel; for now ignore.
      break;

    case "runtime_validation_error":
      console.warn("[Pragma] Runtime smoke test failed:", msg.message);
      runtimeValidationError.set(msg);
      break;

    case "runtime_validation_passed":
      console.log("[Pragma] Runtime smoke test passed");
      runtimeValidationError.set(null);
      break;

    case "spec_progress":
      specProgress.set({ pass: msg.pass, status: msg.status, message: msg.message });
      break;

    case "queued_message":
      // A message was queued during generation — will be applied post-gen
      console.log("[Pragma] Message queued for post-generation refinement");
      break;

    case "refine_project":
      // Server signals that refine mode is available — populate the refinement stores
      if (msg.manifest && msg.spec) {
        checkpointManifest.set(msg.manifest);
        checkpointSpec.set(msg.spec);
        phase.set("refine");
      }
      break;
  }
}

function startElapsedTimer() {
  stopElapsedTimer();
  elapsed.set(0);
  elapsedTimer = setInterval(() => {
    elapsed.set(Math.floor((Date.now() - phaseStart) / 1000));
  }, 1000);
}

function stopElapsedTimer() {
  if (elapsedTimer) {
    clearInterval(elapsedTimer);
    elapsedTimer = null;
  }
}

// ─── Resumability helpers ───

/**
 * On reconnect, fetch the server's current run state and reconcile local stores.
 * If a run is in progress (generating/paused), restore phase + completed files
 * so the UI re-joins mid-run without a full reload.
 */
async function syncServerState() {
  try {
    const res = await fetch("/api/status");
    if (!res.ok) return;
    const data = await res.json();
    if (data.phase && data.phase !== "idle") {
      phase.set(data.phase as Phase);
    }
    if (
      (data.phase === "generating" || data.phase === "paused") &&
      Array.isArray(data.files_completed_list)
    ) {
      filesCompleted.set(
        (data.files_completed_list as string[]).map((p: string) => ({
          path: p,
          healed: false,
          failed: false,
          duration_ms: 0,
        })),
      );
      if (data.total_files > 0) totalFiles.set(data.total_files);
    }
    // Restore spec data and selected profile
    if (data.spec) {
      try {
        const specObj = typeof data.spec === "string" ? JSON.parse(data.spec) : data.spec;
        specData.set(specObj);
        if (specObj.files) specFileCount.set(specObj.files.length);
        if (specObj.tests) specTestCount.set(specObj.tests.length);
      } catch { /* ignore */ }
    }
    if (data.selected_profile) selectedProfile.set(data.selected_profile);
    if (data.manifest) {
      try {
        manifest.set(typeof data.manifest === "string" ? JSON.parse(data.manifest) : data.manifest);
      } catch { /* ignore */ }
    }
  } catch {
    // non-fatal — server may not have /api/status yet
  }
  loadRecentRuns();
}

/**
 * Fetch the list of recent completed runs from the server and populate
 * the recentRuns store (used by the Sidebar to show run history).
 */
export async function loadRecentRuns() {
  try {
    const res = await fetch("/api/runs");
    if (!res.ok) return;
    const data = await res.json();
    if (Array.isArray(data)) recentRuns.set(data as RunSummary[]);
  } catch {
    // ignore — sidebar will just show empty history
  }
}

// ─── Actions (send to server) ───
export function sendMessage(content: string) {
  // Optimistic: add to local messages immediately (fluid feel)
  messages.update((m) => [
    ...m,
    { role: "user", content, timestamp: Date.now() },
  ]);
  send({ action: "send_message", content });
}

export async function startInterview(description: string) {
	// Fail early if WebSocket is not connected — otherwise user clicks Submit
	// and gets stuck in "ideation" with no server response.
	if (!ws || ws.readyState !== WebSocket.OPEN) {
		errorMsg.set("Cannot connect to server. Make sure the daemon is running and try again.");
		phase.set("idle");
		return;
	}

	// Auto-select the best profile from the description via the server
  try {
    const res = await fetch('/api/select-profile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: description })
    });
    if (res.ok) {
      const data = await res.json();
      selectedProfile.set(data.profile);
    }
  } catch {
    // Non-fatal — will use config default on the server
  }

  // Optimistic: transition to ideation immediately, then send the description
  phase.set("ideation");
  messages.update((m) => [
    ...m,
    { role: "user", content: description, timestamp: Date.now() },
  ]);
  send({ action: "send_message", content: description });
}

export function approveSpec() {
  send({ action: "approve_spec" });
}

export function rejectSpec() {
  send({ action: "reject_spec" });
}

export function approveDAG() {
  send({ action: "approve_dag" });
}

export function rejectDAG() {
  send({ action: "reject_dag" });
}

export function pauseRun() {
  send({ action: "pause_run" });
}

export function refineSpec(text: string) {
  send({ action: "refine_spec", content: text });
}

export function resumeRun(runId: string) {
  send({ action: "resume_run", run_id: runId });
}

export function sendUpdateManifest(projectName: string, additions: string) {
  send({ action: "update_manifest", project_name: projectName, additions });
}

let lastAction: any = null;

function send(payload: any) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    lastAction = payload;
    ws.send(JSON.stringify(payload));
  }
}

export function retryLastAction() {
  if (lastAction) {
    errorMsg.set(null);
    phase.set("idle");
    send(lastAction);
  }
}

// ─── Reset (new project) ───
export function resetStores() {
  phase.set("idle");
  messages.set([]);
  interviewDone.set(false);
  manifest.set(null);
  specData.set(null);
  specFileCount.set(0);
  specTestCount.set(0);
  dagSlices.set([]);
  dagEstSeconds.set(0);
  dagEstCost.set("");
  showPreCompile.set(false);
  filesCompleted.set([]);
  totalFiles.set(0);
  runResult.set(null);
  budgetSpent.set(0);
  budgetRemaining.set(0);
  errorMsg.set(null);
  elapsed.set(0);
  reconnectAttempts.set(0);
  reconnectFailed.set(false);
  selectedProfile.set("fastapi-async");
  specProgress.set(null);
  runtimeValidationError.set(null);
  lastAction = null;
}
