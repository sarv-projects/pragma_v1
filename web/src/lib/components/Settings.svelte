<script lang="ts">
	import { refreshHealthStatus } from '$lib/stores/settings';

	let provider = $state('deepseek');
	let apiKey = $state('');
	let baseUrl = $state('');
	let saving = $state(false);
	let error = $state('');
	let success = $state('');
	let keys = $state<Record<string, { configured: boolean; masked: string }>>({});

	const { onClose }: { onClose: () => void } = $props();

	async function loadSettings() {
		try {
			const res = await fetch('/api/settings');
			const data = await res.json();
			keys = data.keys || {};
		} catch {
			// ignore
		}
	}

	function handleClose() {
		refreshHealthStatus();
		onClose();
	}

	async function saveKey() {
		if (!apiKey.trim()) {
			error = 'Please enter an API key';
			return;
		}

		saving = true;
		error = '';
		success = '';

		try {
			// Validate first
			const valRes = await fetch('/api/validate-key', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					provider,
					base_url: baseUrl || undefined,
					api_key: apiKey
				})
			});
			const valData = await valRes.json();

			if (!valData.valid) {
				error = valData.error || 'Validation failed';
				saving = false;
				return;
			}

			// Save
			const saveRes = await fetch('/api/settings', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					provider,
					api_key: apiKey,
					base_url: baseUrl,
					mode: 'fast'
				})
			});

			if (!saveRes.ok) {
				error = 'Failed to save';
				saving = false;
				return;
			}

			success = 'Key saved successfully';
			apiKey = '';
			await loadSettings();
		} catch {
			error = 'Network error';
		} finally {
			saving = false;
		}
	}

	// Load on mount
	$effect(() => {
		loadSettings();
	});
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
	<div class="w-full max-w-md rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-6 shadow-2xl">
		<!-- Header -->
		<div class="mb-6 flex items-center justify-between">
			<h2 class="text-lg font-semibold text-[var(--text-primary)]">Settings</h2>
			<button
				onclick={handleClose}
				aria-label="Close settings"
				class="rounded-md p-1.5 text-[var(--text-dim)] transition-fluid hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
			>
				<svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
				</svg>
			</button>
		</div>

		<!-- Current keys -->
		<div class="mb-6 space-y-2">
			<p class="text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Configured Keys</p>
			{#each Object.entries(keys) as [name, info]}
				<div class="flex items-center justify-between rounded-lg bg-[var(--bg-base)] px-3 py-2">
					<span class="text-sm font-medium capitalize text-[var(--text-primary)]">{name}</span>
					{#if info.configured}
						<span class="font-mono text-xs text-[var(--accent)]">{info.masked}</span>
					{:else}
						<span class="text-xs text-[var(--text-dim)]">Not set</span>
					{/if}
				</div>
			{/each}
		</div>

		<!-- Update key -->
		<div class="space-y-3">
			<p class="text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Update Key</p>

			<select
				bind:value={provider}
				class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--brand)] focus:outline-none"
			>
				<option value="deepseek">DeepSeek</option>
				<option value="groq">Groq</option>
				<option value="openai">OpenAI</option>
				<option value="custom">Custom</option>
			</select>

			{#if provider === 'custom'}
				<input
					type="text"
					bind:value={baseUrl}
					placeholder="Base URL"
					class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none"
				/>
			{/if}

			<input
				type="password"
				bind:value={apiKey}
				placeholder="New API key"
				class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none"
			/>

			{#if error}
				<p class="text-sm text-red-400">{error}</p>
			{/if}

			{#if success}
				<p class="text-sm text-green-400">{success}</p>
			{/if}

			<button
				onclick={saveKey}
				disabled={saving || !apiKey.trim()}
				class="w-full rounded-lg bg-[var(--brand)] px-3 py-2.5 text-sm font-medium text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
			>
				{saving ? 'Saving...' : 'Validate & Save'}
			</button>
		</div>
	</div>
</div>
