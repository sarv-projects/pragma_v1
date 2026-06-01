import { writable, derived } from "svelte/store";

/** Whether the Python daemon is running. */
export const daemonReady = writable<boolean>(false);

/** Raw health data from /api/health */
export const healthData = writable<HealthData | null>(null);

/** True when both DeepSeek and Groq keys are configured (both required). */
export const bothKeysReady = derived(healthData, ($d) =>
  Boolean($d?.checks?.deepseek_key?.ok && $d?.checks?.groq_key?.ok)
);

/** Set to true when the SetupGuide is visible (hides sidebar settings button). */
export const guideActive = writable<boolean>(false);

export interface HealthCheck {
  ok: boolean;
  message: string;
}

export interface HealthData {
  checks: {
    python: HealthCheck;
    daemon: HealthCheck;
    deepseek_key: HealthCheck;
    groq_key: HealthCheck;
    docker: HealthCheck;
  };
  is_wsl: boolean;
  port: string;
  all_ok: boolean;
}

/** Fetch health status from the server. */
export async function refreshHealthStatus(): Promise<void> {
  try {
    const res = await fetch("/api/health");
    if (res.ok) {
      const data: HealthData = await res.json();
      healthData.set(data);
      daemonReady.set(data.checks?.daemon?.ok ?? false);
    }
  } catch {
    daemonReady.set(false);
  }
}
