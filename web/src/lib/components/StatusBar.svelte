<script lang="ts">
	import { phase, budgetSpent, budgetRemaining, elapsed, errorMsg } from '$lib/stores/ws';

	function formatElapsed(s: number): string {
		if (s < 60) return `${s}s`;
		return `${Math.floor(s / 60)}m ${s % 60}s`;
	}

	function phaseLabel(p: string): string {
		const labels: Record<string, string> = {
			idle: 'Ready',
			ideation: 'Ideation',
			researching: 'Researching',
			compiling_spec: 'Compiling Spec',
			spec_review: 'Spec Review',
			dag_review: 'Execution Plan',
			generating: 'Generating',
			complete: 'Complete',
			paused: 'Paused',
			error: 'Error'
		};
		return labels[p] || p;
	}
</script>

<footer class="flex items-center justify-between border-t border-[var(--border)] bg-[var(--bg-raised)] px-4 py-2 text-xs">
	<div class="flex items-center gap-4">
		<span class="rounded-md bg-[var(--brand)] px-2 py-0.5 font-semibold text-white">PRAGMA</span>
		<span class="text-[var(--text-muted)]">
			Phase: <span class="font-medium text-[var(--text-primary)]">{phaseLabel($phase)}</span>
		</span>
		{#if $phase !== 'idle' && $phase !== 'complete'}
			<span class="text-[var(--text-dim)]">{formatElapsed($elapsed)}</span>
		{/if}
	</div>

	<div class="flex items-center gap-4">
		{#if $errorMsg}
			<span class="text-[var(--error)]">⚠ {$errorMsg}</span>
		{/if}
		<span class="text-[var(--text-muted)]">
			Cost: <span class="text-[var(--accent)]">${$budgetSpent.toFixed(3)}</span>
		</span>
		<span class="text-[var(--text-muted)]">
			Left: <span class="font-medium text-[var(--text-primary)]">${$budgetRemaining.toFixed(2)}</span>
		</span>
	</div>
</footer>
