<script lang="ts">
	import { runResult, resetStores, phase, manifest } from '$lib/stores/ws';
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
</script>

<div class="flex h-full items-center justify-center px-4 overflow-y-auto py-8">
	<div class="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-[var(--bg-raised)] p-8 shadow-xl enter-from-below my-auto">
		{#if $runResult}
			<div class="mb-6 text-center">
				<div class="mb-3 text-4xl">🎉</div>
				<h2 class="text-2xl font-bold text-[var(--text-primary)]">Build Complete</h2>
			</div>

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
