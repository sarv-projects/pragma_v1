<script lang="ts">
	import { runResult, resetStores, phase, manifest, runtimeValidationError } from '$lib/stores/ws';
	import { checkpointManifest, checkpointSpec } from '$lib/stores/refine';

	// Request notification permission on mount
	$effect(() => {
		if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
			Notification.requestPermission();
		}
	});

	// Fire notification when result arrives
	$effect(() => {
		if ($runResult && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
			new Notification('Your project is ready!', {
				body: `${$runResult.project_name} - ${$runResult.file_count} files generated`
			});
		}
	});

	let readmeContent = $state('');
	let runningDocker = $state(false);
	let dockerOutput = $state('');
	let dockerError = $state('');
	let previewActive = $state(false);
	let previewUrl = $state('');
	let previewLoading = $state(false);
	let previewError = $state('');

	$effect(() => {
		const controller = new AbortController();
		if ($runResult?.output_path) {
			const runId = $runResult.output_path.split(/[/\\]/).pop();
			if (runId) {
				fetch(`/api/readme?run_id=${runId}`, { signal: controller.signal })
					.then((res) => res.text())
					.then((text) => {
						const lines = text.split('\n');
						readmeContent = lines.slice(0, 12).join('\n') + (lines.length > 12 ? '\n…' : '');
					})
					.catch((err) => { 
						if (err.name !== 'AbortError') {
							readmeContent = ''; 
						}
					});
			}
		}
		return () => controller.abort();
	});

	function newProject() {
		resetStores();
	}

	function openFolder() {
		if (!$runResult) return;
		fetch('/api/open-folder', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ path: $runResult.output_path })
		});
	}

	async function runWithDocker() {
		if (!$runResult) return;
		const runId = $runResult.output_path.split(/[/\\]/).pop();
		if (!runId) return;
		runningDocker = true;
		dockerError = '';
		dockerOutput = '';
		try {
			const res = await fetch('/api/run-project', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ run_id: runId })
			});
			const data = await res.json();
			if (!res.ok) {
				dockerError = data.error || 'Failed to start Docker';
			} else {
				dockerOutput = data.output || 'Started!';
			}
		} catch {
			dockerError = 'Network error';
		} finally {
			runningDocker = false;
		}
	}

	async function startPreview() {
		if (!$runResult) return;
		const runId = $runResult.output_path.split(/[/\\]/).pop();
		if (!runId) return;
		previewLoading = true;
		previewError = '';
		try {
			const res = await fetch('/api/preview/start', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ run_id: runId })
			});
			const data = await res.json();
			if (!res.ok) {
				previewError = data.error || 'Failed to start preview';
			} else {
				previewActive = true;
				previewUrl = data.preview_url;
			}
		} catch {
			previewError = 'Network error';
		} finally {
			previewLoading = false;
		}
	}

	async function stopPreview() {
		try {
			await fetch('/api/preview/stop', { method: 'POST' });
		} catch { /* ignore */ }
		previewActive = false;
		previewUrl = '';
	}
</script>

<div class="flex h-full items-center justify-center px-4 overflow-y-auto py-8">
	<div class="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-[var(--bg-raised)] p-8 shadow-xl enter-from-below my-auto">
		{#if $runResult}
			<div class="mb-6 text-center">
				<div class="mb-3 text-4xl">🎉</div>
				<h2 class="text-2xl font-bold text-[var(--text-primary)]">Build Complete</h2>
			</div>

			<!-- Runtime validation status -->
			{#if $runtimeValidationError}
				<div class="mb-4 rounded-lg bg-[var(--bg-base)] border border-yellow-500/30 p-3 text-sm" role="alert" aria-live="polite">
					<p class="text-yellow-400 font-medium">⚠️ Quick health check found issues</p>
					<p class="text-[var(--text-muted)] text-xs mt-1">{$runtimeValidationError.message}</p>
					{#if $runtimeValidationError.logs}
						<details class="mt-2 text-xs text-[var(--text-dim)]">
							<summary>Show error details</summary>
							<pre class="mt-1 whitespace-pre-wrap overflow-x-auto max-h-40">{$runtimeValidationError.logs}</pre>
						</details>
					{/if}
				</div>
			{/if}

			<div class="mb-6 space-y-3 text-sm">
				<div class="flex justify-between">
					<span class="text-[var(--text-muted)]">Project</span>
					<span class="font-medium text-[var(--text-primary)]">{$runResult.project_name}</span>
				</div>
				<div class="flex justify-between">
					<span class="text-[var(--text-muted)]">Output</span>
					<span class="font-mono text-xs text-[var(--accent)]">{$runResult.output_path}</span>
				</div>
				<div class="flex justify-between">
					<span class="text-[var(--text-muted)]">Files</span>
					<span class="text-[var(--text-primary)]">
						{$runResult.file_count} generated · {$runResult.healed} healed · {$runResult.failed} failed
					</span>
				</div>
				<div class="flex justify-between">
					<span class="text-[var(--text-muted)]">Coverage</span>
					<span class="{$runResult.coverage >= 100 ? 'text-[var(--success)]' : 'text-[var(--warning)]'}">
						{$runResult.coverage}%
					</span>
				</div>
				<div class="flex justify-between">
					<span class="text-[var(--text-muted)]">Cost</span>
					<span class="text-[var(--accent)]">${$runResult.cost.toFixed(4)}</span>
				</div>
			</div>

			<!-- Primary CTAs -->
			<div class="mb-3 grid grid-cols-2 gap-3">
				<button
					onclick={openFolder}
					class="rounded-xl bg-[var(--brand)] py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110"
				>
					&#128194; Open Folder
				</button>
				<button
					onclick={runWithDocker}
					disabled={runningDocker}
					class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] py-3 text-sm font-semibold text-[var(--text-primary)] transition-fluid hover:bg-[var(--bg-hover)] disabled:opacity-50"
				>
					{runningDocker ? '&#9203; Starting…' : '&#128051; Run with Docker'}
				</button>
			</div>

			{#if dockerOutput}
				<div class="mb-3 rounded-lg bg-[var(--bg-base)] border border-green-500/30 p-3 text-xs text-green-400 font-mono">
					<p class="font-semibold mb-1 text-green-300 font-sans">&#x2705; Docker started</p>
					<pre class="whitespace-pre-wrap">{dockerOutput}</pre>
					<p class="mt-2 text-[var(--text-muted)] font-sans">Check your README for the exact port. Usually <a href="http://localhost:8000" target="_blank" class="text-[var(--brand-light)] underline">http://localhost:8000</a>.</p>
				</div>
			{/if}
			{#if dockerError}
				<div class="mb-3 rounded-lg bg-[var(--bg-base)] border border-red-500/30 p-3 text-xs text-red-400">
					<p class="font-semibold mb-1">&#x274C; Docker error:</p>
					<p>{dockerError}</p>
					{#if dockerError.includes('Docker not found')}
						<a href="https://www.docker.com/products/docker-desktop/" target="_blank" rel="noopener noreferrer" class="mt-1 block text-[var(--brand-light)] underline">Install Docker Desktop &#x2192;</a>
					{/if}
				</div>
			{/if}

			<!-- Live Preview -->
			{#if !previewActive}
				<div class="mb-3">
					<button
						onclick={startPreview}
						disabled={previewLoading}
						class="w-full rounded-xl bg-[var(--accent)] py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110 disabled:opacity-50"
					>
						{previewLoading ? '&#9203; Starting preview...' : '&#9654; Live Preview'}
					</button>
					{#if previewError}
						<p class="mt-1 text-xs text-red-400">{previewError}</p>
					{/if}
				</div>
			{:else}
				<div class="mb-3 rounded-xl overflow-hidden border border-[var(--border)]">
					<div class="flex items-center justify-between bg-[var(--bg-base)] px-3 py-2">
						<span class="text-xs text-[var(--accent)] font-mono">{previewUrl}</span>
						<button
							onclick={stopPreview}
							class="rounded px-2 py-1 text-xs text-red-400 hover:bg-[var(--bg-hover)]"
						>
							Stop
						</button>
					</div>
					<iframe
						src={previewUrl}
						title="Live Preview"
						class="w-full bg-white"
						style="height: 400px;"
						sandbox="allow-scripts allow-same-origin allow-forms"
					></iframe>
				</div>
			{/if}

			<!-- Download -->
			<div class="mb-4">
				<a
					href="/api/download/{$runResult.output_path.split(/[/\\]/).pop()}"
					download
					class="block w-full rounded-xl bg-[var(--bg-base)] border border-[var(--border)] py-2 text-sm font-medium text-[var(--text-primary)] text-center transition-fluid hover:bg-[var(--bg-hover)]"
				>
					&#11015; Download ZIP
				</a>
			</div>

			{#if readmeContent}
				<div class="mb-4 rounded-lg bg-[var(--bg-base)] p-4 text-sm text-[var(--text-muted)]">
					<p class="mb-2 font-medium text-[var(--text-primary)]">README.md (Preview):</p>
					<pre class="whitespace-pre-wrap font-mono text-xs overflow-x-auto">{readmeContent}</pre>
				</div>
			{:else}
				<div class="mb-4 rounded-lg bg-[var(--bg-base)] p-4 text-sm text-[var(--text-muted)] space-y-1">
					<p class="font-medium text-[var(--text-primary)] mb-2">Next steps:</p>
					<p>1. Click <strong>Open Folder</strong> above to see your code</p>
					<p>2. Click <strong>Run with Docker</strong> to start the app (needs Docker Desktop)</p>
					<p>3. Read <code class="text-[var(--accent)] font-mono">README.md</code> for API docs and setup notes</p>
				</div>
			{/if}

			<button
				onclick={newProject}
				class="w-full rounded-xl border border-[var(--border)] py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]"
			>
				Start New Project
			</button>
			<button
				onclick={() => {
					if (!$runResult || !$manifest) return;
					checkpointManifest.set($manifest as Record<string, any>);
					checkpointSpec.set({ files: [] }); // simplified: just use runResult as base
					const runId = $runResult.output_path.split(/[/\\]/).pop();
					if (runId) {
						fetch('/api/notify-refine', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ run_id: runId }) }).catch(() => {});
					}
					phase.set('refine');
				}}
				class="w-full rounded-xl border border-[var(--brand)]/30 bg-[var(--brand)]/10 py-3 text-sm font-medium text-[var(--brand)] transition-fluid hover:bg-[var(--brand)]/20"
			>
				Refine This Project
			</button>
		{:else}
			<p class="text-center text-[var(--text-muted)]">Run complete!</p>
		{/if}
	</div>
</div>
