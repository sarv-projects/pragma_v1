<script lang="ts">
	import { page } from '$app/stores';
</script>

<svelte:head>
	<title>Error - Pragma</title>
</svelte:head>

<div class="flex min-h-screen flex-col items-center justify-center bg-[#0a0a0f] px-4 text-center text-white">
	<div class="max-w-md space-y-4">
		<h1 class="text-4xl font-bold text-red-400">
			{$page.status === 404 ? 'Page Not Found' : 'Something went wrong'}
		</h1>
		<p class="text-gray-400">
			{$page.error?.message || 'An unexpected error occurred while loading the application.'}
		</p>
		<a
			href="/"
			class="inline-block rounded-md bg-indigo-600 px-6 py-3 font-medium text-white transition-colors hover:bg-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2 focus:ring-offset-[#0a0a0f]"
		>
			Return to Home
		</a>
	</div>
</div>
