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
	<!-- Sidebar -->
	<Sidebar />

	<!-- Main content area -->
	<main class="flex flex-1 flex-col overflow-hidden">
		<DisconnectedBanner />
		<div class="flex-1 overflow-y-auto">
			{@render children()}
		</div>
		<StatusBar />
	</main>
</div>
