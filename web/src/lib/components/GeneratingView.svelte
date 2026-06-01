<script lang="ts">
	import { filesCompleted, totalFiles, progress, elapsed } from '$lib/stores/ws';
	import { tick } from 'svelte';

	let listEl: HTMLDivElement;

	// Auto-scroll file list
	$effect(() => {
		if ($filesCompleted.length && listEl) {
			tick().then(() => listEl.scrollTo({ top: listEl.scrollHeight, behavior: 'smooth' }));
		}
	});

	let pct = $derived(Math.round($progress * 100));
	let speed = $derived.by(() => {
		if ($elapsed < 2 || $filesCompleted.length === 0) return '—';
		return ($filesCompleted.length / $elapsed).toFixed(1) + ' files/s';
	});
	let eta = $derived.by(() => {
		if ($filesCompleted.length === 0 || $elapsed < 2) return '—';
		const perFile = $elapsed / $filesCompleted.length;
		const remaining = ($totalFiles - $filesCompleted.length) * perFile;
		return remaining < 60 ? `~${Math.round(remaining)}s` : `~${Math.round(remaining / 60)}m`;
	});
</script>

<div class="flex h-full flex-col px-4 py-6 md:px-8">
	<div class="mx-auto w-full max-w-3xl">
		<h2 class="mb-4 text-2xl font-bold text-[var(--text-primary)]">Generating</h2>

		<!-- Progress bar (GPU-animated via transform scaleX) -->
		<div class="mb-3 h-2.5 overflow-hidden rounded-full bg-[var(--bg-base)] border border-[var(--border)]">
			<div
				class="h-full rounded-full bg-gradient-to-r from-[var(--brand)] to-[var(--accent)] origin-left transition-transform duration-300 ease-out"
				style="transform: scaleX({$progress})"
			></div>
		</div>

		<!-- Stats row -->
		<div class="mb-4 flex flex-wrap gap-4 text-xs text-[var(--text-muted)]">
			<span><span class="font-semibold text-[var(--text-primary)]">{$filesCompleted.length}</span> / {$totalFiles} files</span>
			<span>{pct}%</span>
			<span>Speed: {speed()}</span>
			<span>ETA: {eta()}</span>
		</div>

		<!-- File list -->
		<div bind:this={listEl} class="mb-4 max-h-[50vh] overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-1">
			{#each $filesCompleted as f, i (f.path)}
				<div class="flex items-center gap-2 text-sm enter-from-below" style="animation-delay: {Math.min(i * 20, 100)}ms">
					{#if f.failed}
						<span class="text-[var(--error)]">✗</span>
					{:else if f.healed}
						<span class="text-[var(--warning)]">✚</span>
					{:else}
						<span class="text-[var(--success)]">✓</span>
					{/if}
					<span class="text-[var(--text-primary)]">{f.path}</span>
					<span class="text-[var(--text-dim)] text-xs">{(f.duration_ms / 1000).toFixed(1)}s</span>
				</div>
			{/each}
			{#if $filesCompleted.length === 0}
				<p class="text-sm text-[var(--text-dim)] animate-[pulse-soft_2s_infinite]">Waiting for first file…</p>
			{/if}
		</div>
	</div>
</div>
