<script lang="ts">
	import { phase, budgetRemaining, resetStores, recentRuns } from '$lib/stores/ws';
	import { guideActive } from '$lib/stores/settings';
	import Settings from '$lib/components/Settings.svelte';

	let collapsed = $state(false);
	let showSettings = $state(false);
	let showHelp = $state(false);

	function newProject() {
		resetStores();
		// Navigate to home/chat
		window.location.hash = '';
	}
</script>

<aside
	class="flex flex-col border-r border-[var(--border)] bg-[var(--bg-raised)] transition-fluid {collapsed ? 'w-16' : 'w-64'}"
>
	<!-- Logo -->
	<div class="flex items-center gap-2 px-4 py-5">
		{#if !collapsed}
			<span class="text-lg font-bold text-[var(--brand-light)]">▰ Pragma</span>
		{:else}
			<span class="text-lg font-bold text-[var(--brand-light)]">▰</span>
		{/if}
	</div>

	<!-- New Project -->
	<button
		onclick={newProject}
		class="mx-3 mb-4 flex items-center gap-2 rounded-lg bg-[var(--brand)] px-3 py-2.5 text-sm font-medium text-white transition-fluid hover:scale-[1.02] hover:brightness-110 active:scale-[0.98]"
	>
		<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
			<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
		</svg>
		{#if !collapsed}<span>New Project</span>{/if}
	</button>

	<!-- Recent runs -->
	<div class="flex-1 overflow-y-auto px-3">
		{#if !collapsed}
			<p class="mb-2 text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Recent</p>
			<div class="space-y-1">
				{#if $recentRuns.length > 0}
					{#each $recentRuns as run (run.run_id)}
						<div class="rounded-lg px-3 py-2 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]">
							{run.project_name || run.run_id}
						</div>
					{/each}
				{:else}
					<div class="rounded-lg px-3 py-2 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]">
						No projects yet
					</div>
				{/if}
			</div>
		{/if}
	</div>

	<!-- Bottom: settings + budget -->
	<div class="border-t border-[var(--border)] px-3 py-3">
		{#if !collapsed}
			<div class="flex items-center justify-between text-xs text-[var(--text-muted)]">
				<span>Budget</span>
				<span class="font-medium text-[var(--accent)]">${$budgetRemaining.toFixed(2)}</span>
			</div>
		{/if}
		{#if !$guideActive}
			<button
				onclick={() => (showSettings = true)}
				aria-label="Settings"
				class="mt-2 flex w-full items-center gap-2 rounded-md p-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
			>
				<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.573-1.066z" />
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
				</svg>
				{#if !collapsed}<span>Settings</span>{/if}
			</button>
		{/if}
		<!-- Help button -->
		<div class="relative">
			<button
				onclick={() => (showHelp = !showHelp)}
				aria-label="Help"
				class="mt-2 flex w-full items-center gap-2 rounded-md p-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
			>
				<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8.228 9c.549-1.165 2.03-2 3.772-2 2.21 0 4 1.343 4 3 0 1.4-1.278 2.575-3.006 2.907-.542.104-.994.54-.994 1.093m0 3h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
				{#if !collapsed}<span>Help</span>{/if}
			</button>
			{#if showHelp}
				<div class="absolute bottom-full left-0 mb-2 w-56 rounded-lg border border-[var(--border)] bg-[var(--bg-raised)] p-3 shadow-xl z-50">
					<p class="mb-2 text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Help</p>
					<ul class="space-y-2 text-sm">
						<li>
							<a href="https://github.com/sarv-projects/pragma" target="_blank" rel="noopener noreferrer" class="text-[var(--brand-light)] hover:underline">Documentation</a>
						</li>
						<li>
							<a href="https://github.com/sarv-projects/pragma/issues" target="_blank" rel="noopener noreferrer" class="text-[var(--brand-light)] hover:underline">Report an issue</a>
						</li>
						<li class="border-t border-[var(--border)] pt-2">
							<p class="text-xs text-[var(--text-dim)] mb-1">Keyboard shortcuts</p>
							<p class="text-xs text-[var(--text-muted)]"><kbd class="rounded bg-[var(--bg-base)] px-1">Ctrl+C</kbd> Stop</p>
							<p class="text-xs text-[var(--text-muted)]"><kbd class="rounded bg-[var(--bg-base)] px-1">Cmd+Enter</kbd> Submit</p>
						</li>
					</ul>
				</div>
			{/if}
		</div>
		<button
			onclick={() => (collapsed = !collapsed)}
			aria-label="Toggle sidebar"
			class="mt-2 flex w-full items-center justify-center rounded-md p-1.5 text-[var(--text-dim)] transition-fluid hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
		>
			<svg class="h-4 w-4 transition-fluid {collapsed ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
			</svg>
		</button>
	</div>
</aside>

{#if showSettings}
	<Settings onClose={() => (showSettings = false)} />
{/if}
