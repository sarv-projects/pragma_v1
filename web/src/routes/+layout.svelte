<script lang="ts">
	import '../app.css';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import StatusBar from '$lib/components/StatusBar.svelte';
	import DisconnectedBanner from '$lib/components/DisconnectedBanner.svelte';
	import { connect } from '$lib/stores/ws';
	import { onMount } from 'svelte';

	let { children } = $props();

	onMount(() => {
		connect();
	});
</script>

<div class="flex h-screen w-screen overflow-hidden">
	<!-- Skip to content link for accessibility -->
	<a href="#main-content" class="sr-only focus:not-sr-only focus:absolute focus:z-[100] focus:top-2 focus:left-2 focus:rounded-lg focus:bg-[var(--brand)] focus:px-4 focus:py-2 focus:text-white focus:text-sm">
		Skip to main content
	</a>

	<!-- Sidebar -->
	<Sidebar />

	<!-- Main content area -->
	<main id="main-content" class="flex flex-1 flex-col overflow-hidden">
		<DisconnectedBanner />
		<div class="flex-1 overflow-y-auto">
			{@render children()}
		</div>
		<StatusBar />
	</main>
</div>
