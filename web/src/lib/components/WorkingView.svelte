<script lang="ts">
	import { phase, elapsed } from '$lib/stores/ws';

	function phaseInfo(p: string): { title: string; subtitle: string } {
		if (p === 'researching') return {
			title: 'Researching',
			subtitle: 'Querying DeepWiki + DuckDuckGo for library patterns…'
		};
		return {
			title: 'Compiling the Build Contract',
			subtitle: 'Reasoning over architecture, then refining. This usually takes 30-60 seconds.'
		};
	}

	function formatElapsed(s: number): string {
		if (s < 60) return `${s}s`;
		return `${Math.floor(s / 60)}m ${s % 60}s`;
	}

	let info = $derived(phaseInfo($phase));
</script>

<div class="flex h-full items-center justify-center px-4">
	<div class="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--bg-raised)] p-8 text-center shadow-xl enter-from-below">
		<!-- Animated spinner -->
		<div class="mx-auto mb-6 h-12 w-12">
			<div class="h-full w-full animate-spin rounded-full border-[3px] border-[var(--border)] border-t-[var(--brand)]"></div>
		</div>

		<h2 class="mb-2 text-xl font-semibold text-[var(--text-primary)]">{info.title}</h2>
		<p class="mb-4 text-sm text-[var(--text-muted)]">{info.subtitle}</p>
		<p class="text-xs text-[var(--text-dim)]">Elapsed: {formatElapsed($elapsed)}</p>
	</div>
</div>
