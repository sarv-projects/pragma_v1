<script lang="ts">
	import { filesCompleted, totalFiles, progress, elapsed, sendMessage } from '$lib/stores/ws';
	import { tick } from 'svelte';

	let listEl: HTMLDivElement;
	let chatInput = $state('');
	let queuedCount = $state(0);
	let sending = $state(false);

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

	function queueMessage() {
		if (!chatInput.trim() || sending) return;
		sending = true;
		sendMessage(chatInput.trim());
		queuedCount++;
		chatInput = '';
		setTimeout(() => { sending = false; }, 300);
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			queueMessage();
		}
	}
</script>

<div class="flex h-full flex-col px-4 py-6 md:px-8">
	<div class="mx-auto w-full max-w-3xl flex-1 flex flex-col min-h-0">
		<h2 class="mb-4 text-2xl font-bold text-[var(--text-primary)]">Generating</h2>

		<!-- Progress bar (GPU-animated via transform scaleX) -->
		<div class="mb-3 h-2.5 overflow-hidden rounded-full bg-[var(--bg-base)] border border-[var(--border)]" role="progressbar" aria-valuenow={Math.round($progress * 100)} aria-valuemin="0" aria-valuemax="100" aria-label="Generation progress">
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
			{#if queuedCount > 0}
				<span class="text-[var(--accent)]">{queuedCount} message{queuedCount > 1 ? 's' : ''} queued</span>
			{/if}
		</div>

		<!-- File list -->
		<div bind:this={listEl} class="mb-4 flex-1 min-h-0 max-h-[40vh] overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-1">
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

		<!-- Chat input for mid-generation messages -->
		<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-3">
			<p class="mb-2 text-xs text-[var(--text-dim)]">
				💡 Forgot something? Send a note — it'll be applied after generation completes.
			</p>
			<div class="flex gap-2">
				<textarea
					bind:value={chatInput}
					onkeydown={handleKeydown}
					placeholder="e.g., Add dark mode support, use bcrypt for passwords..."
					rows="2"
					class="flex-1 resize-none rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none"
				></textarea>
				<button
					onclick={queueMessage}
					disabled={!chatInput.trim() || sending}
					class="self-end rounded-lg bg-[var(--brand)] px-4 py-2 text-sm font-medium text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
				>
					Queue
				</button>
			</div>
		</div>
	</div>
</div>
