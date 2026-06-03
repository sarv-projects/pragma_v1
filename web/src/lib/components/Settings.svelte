<script lang="ts">
	import { refreshHealthStatus } from '$lib/stores/settings';

	let provider = $state('deepseek');
	let apiKey = $state('');
	let baseUrl = $state('');
	let reasoningModel = $state('');
	let codegenModel = $state('');
	let supportsThinking = $state(true);
	let saving = $state(false);
	let error = $state('');
	let success = $state('');
	let keys = $state<Record<string, { configured: boolean; masked: string }>>({});
	let discoveredModels = $state<string[]>([]);

	const { onClose }: { onClose: () => void } = $props();

	const providers = [
		{ id: 'deepseek', name: 'DeepSeek', url: 'https://api.deepseek.com', thinking: true },
		{ id: 'openai', name: 'OpenAI', url: 'https://api.openai.com/v1', thinking: true },
		{ id: 'openrouter', name: 'OpenRouter', url: 'https://openrouter.ai/api/v1', thinking: false },
		{ id: 'together', name: 'Together AI', url: 'https://api.together.xyz/v1', thinking: false },
		{ id: 'ollama', name: 'Ollama (Local)', url: 'http://localhost:11434/v1', thinking: false },
		{ id: 'custom', name: 'Custom (BYOK)', url: '', thinking: false },
	];

	function getProviderInfo(id: string) {
		return providers.find(p => p.id === id) || providers[providers.length - 1];
	}

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

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			handleClose();
		}
	}

	function onProviderChange() {
		const info = getProviderInfo(provider);
		baseUrl = info.url;
		supportsThinking = info.thinking;
		discoveredModels = [];
		reasoningModel = '';
		codegenModel = '';
	}

	async function validateAndDiscover() {
		if (!apiKey.trim()) {
			error = 'Please enter an API key';
			return;
		}

		saving = true;
		error = '';
		success = '';
		discoveredModels = [];

		try {
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

			discoveredModels = valData.models || [];
			success = `Valid! Found ${discoveredModels.length} models.`;
		} catch {
			error = 'Network error';
		} finally {
			saving = false;
		}
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
			const saveRes = await fetch('/api/settings', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					provider,
					api_key: apiKey,
					base_url: baseUrl,
					mode: 'fast',
					reasoning_model: reasoningModel || undefined,
					codegen_model: codegenModel || undefined,
					supports_thinking: supportsThinking
				})
			});

			if (!saveRes.ok) {
				error = 'Failed to save';
				saving = false;
				return;
			}

			success = 'Settings saved successfully';
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

<svelte:window on:keydown={handleKeydown} />

<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label="Settings">
	<div class="w-full max-w-lg rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-6 shadow-2xl max-h-[85vh] overflow-y-auto">
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

		<!-- Provider selection -->
		<div class="space-y-3">
			<p class="text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Codegen Provider</p>
			<p class="text-xs text-[var(--text-dim)]">Groq is always used for interview & healing. Choose your code generation provider below.</p>

			<select
				bind:value={provider}
				onchange={onProviderChange}
				class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--brand)] focus:outline-none"
			>
				{#each providers as p}
					<option value={p.id}>{p.name}</option>
				{/each}
			</select>

			{#if provider === 'custom' || provider === 'ollama'}
				<input
					type="text"
					bind:value={baseUrl}
					placeholder="Base URL (e.g., http://localhost:11434/v1)"
					class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none"
				/>
			{/if}

			<input
				type="password"
				bind:value={apiKey}
				placeholder="API key (not needed for local Ollama)"
				class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none"
			/>

			<button
				onclick={validateAndDiscover}
				disabled={saving || !apiKey.trim()}
				class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] transition-fluid hover:bg-[var(--bg-hover)] disabled:opacity-50 disabled:cursor-not-allowed"
			>
				{saving ? 'Validating...' : 'Validate & Discover Models'}
			</button>

			{#if discoveredModels.length > 0}
				<div class="space-y-2">
					<p class="text-xs text-[var(--accent)]">Discovered {discoveredModels.length} models</p>

					<label class="block text-xs text-[var(--text-dim)]">
						Reasoning Model (for spec compilation)
						<select
							bind:value={reasoningModel}
							class="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--brand)] focus:outline-none"
						>
							<option value="">Auto-detect</option>
							{#each discoveredModels as m}
								<option value={m}>{m}</option>
							{/each}
						</select>
					</label>

					<label class="block text-xs text-[var(--text-dim)]">
						Codegen Model (for file generation)
						<select
							bind:value={codegenModel}
							class="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--brand)] focus:outline-none"
						>
							<option value="">Auto-detect</option>
							{#each discoveredModels as m}
								<option value={m}>{m}</option>
							{/each}
						</select>
					</label>

					<label class="flex items-center gap-2 text-xs text-[var(--text-dim)]">
						<input type="checkbox" bind:checked={supportsThinking} class="rounded" />
						Model supports thinking/reasoning mode
					</label>
				</div>
			{/if}

			{#if error}
				<p class="text-sm text-red-400">{error}</p>
			{/if}

			{#if success}
				<p class="text-sm text-green-400">{success}</p>
			{/if}

			<button
				onclick={saveKey}
				disabled={saving}
				class="w-full rounded-lg bg-[var(--brand)] px-3 py-2.5 text-sm font-medium text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
			>
				{saving ? 'Saving...' : 'Save Settings'}
			</button>
		</div>
	</div>
</div>
