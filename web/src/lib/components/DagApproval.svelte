<script lang="ts">
	import { dagSlices, dagEstSeconds, dagEstCost, approveDAG, rejectDAG } from '$lib/stores/ws';

	let showFileList = $state(false);

	let totalFiles = $derived($dagSlices.reduce((n, s) => n + s.length, 0));
</script>

<div class="flex h-full flex-col px-4 py-6 md:px-8">
	<div class="mx-auto w-full max-w-3xl">
		<h2 class="mb-1 text-2xl font-bold text-[var(--text-primary)]">Ready to build</h2>
		<p class="mb-6 text-sm text-[var(--text-muted)]">
			Pragma has a plan. Review it, then hit go.
		</p>

		<!-- Simple summary card -->
		<div class="mb-5 rounded-xl border border-[var(--brand)]/25 bg-[var(--brand)]/5 p-5">
			<div class="grid grid-cols-3 gap-4 mb-5 text-center">
				<div>
					<p class="text-2xl font-bold text-[var(--brand-light)]">{totalFiles}</p>
					<p class="text-xs text-[var(--text-dim)] mt-0.5">files</p>
				</div>
				<div>
					<p class="text-2xl font-bold text-[var(--text-primary)]">{$dagSlices.length}</p>
					<p class="text-xs text-[var(--text-dim)] mt-0.5">batches</p>
				</div>
				<div>
					<p class="text-2xl font-bold text-[var(--accent)]">
						{$dagEstCost || '~$0.03'}
					</p>
					<p class="text-xs text-[var(--text-dim)] mt-0.5">estimated cost</p>
				</div>
			</div>

			{#if $dagEstSeconds > 0}
				<p class="text-center text-xs text-[var(--text-dim)] mb-4">
					~{$dagEstSeconds}s estimated &middot; 20 files in parallel
				</p>
			{/if}

			<div class="flex gap-3">
				<button
					onclick={approveDAG}
					class="flex-1 rounded-xl bg-[var(--brand)] py-3 text-sm font-semibold text-white transition-fluid hover:scale-[1.01] hover:brightness-110 active:scale-[0.99]"
				>
					&#9889; Start Generating
				</button>
				<button
					onclick={rejectDAG}
					class="rounded-xl border border-[var(--border)] px-6 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] active:scale-[0.98]"
				>
					Cancel
				</button>
			</div>
		</div>

		<!-- Collapsible file list (advanced) -->
		<button
			onclick={() => (showFileList = !showFileList)}
			class="mb-3 flex items-center gap-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]"
		>
			<svg class="h-4 w-4 transition-transform {showFileList ? 'rotate-90' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
			</svg>
			{showFileList ? 'Hide' : 'See'} file list ({totalFiles} files)
		</button>

		{#if showFileList}
			<div class="max-h-[45vh] overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4 space-y-4">
				{#each $dagSlices as slice, i}
					<div class="enter-from-below" style="animation-delay: {i * 40}ms">
						<div class="mb-1 flex items-center gap-2">
							<span class="text-xs font-semibold text-[var(--brand-light)]">Batch {i + 1}</span>
							{#if slice.length > 1}
								<span class="text-xs text-[var(--text-dim)]">({slice.length} files in parallel)</span>
							{/if}
						</div>
						<div class="ml-4 space-y-0.5">
							{#each slice as file}
								<p class="text-sm text-[var(--text-muted)]">· {file}</p>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
