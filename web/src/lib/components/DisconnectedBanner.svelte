<script lang="ts">
	import { connected, reconnectFailed, connect } from '$lib/stores/ws';
</script>

{#if !$connected}
	<div class="enter-from-below border-b border-[var(--border)] bg-[var(--bg-raised)] px-4 py-2">
		<div class="mx-auto flex max-w-4xl items-center justify-center gap-2 text-sm text-[var(--warning)]">
			<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
			</svg>
			{#if $reconnectFailed}
				<span>Connection lost.</span>
				<button
					onclick={() => { connect(); }}
					class="underline hover:text-[var(--fg)] transition-colors cursor-pointer"
				>
					Click to retry.
				</button>
			{:else}
				<span>Reconnecting...</span>
			{/if}
		</div>
	</div>
{/if}
