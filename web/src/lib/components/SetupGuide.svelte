<script lang="ts">
	import { fly } from 'svelte/transition';
	import { refreshHealthStatus, healthData, daemonReady } from '$lib/stores/settings';

	const { onComplete }: { onComplete: () => void } = $props();

	// Guide navigation
	let step = $state(1);
	let direction = $state<'forward' | 'backward'>('forward');

	// Key inputs
	let deepseekKey = $state('');
	let groqKey = $state('');

	// Validation state
	let deepseekValid = $state(false);
	let groqValid = $state(false);
	let deepseekSaving = $state(false);
	let groqSaving = $state(false);
	let keyError = $state('');

	// Completion state
	let guideDone = $state(false);

	// Setup check polling
	let checkInterval: ReturnType<typeof setInterval> | null = null;

	$effect(() => {
		if (step === 4) {
			// Start polling health when we reach the setup check step
			refreshHealthStatus();
			checkInterval = setInterval(() => {
				refreshHealthStatus();
			}, 2000);
		} else {
			if (checkInterval) {
				clearInterval(checkInterval);
				checkInterval = null;
			}
		}
	});

	// Auto-advance to step 5 when daemon is ready
	$effect(() => {
		if (step === 4 && $daemonReady) {
			// Small delay so the user sees the green checkmark
			const t = setTimeout(() => {
				direction = 'forward';
				step = 5;
			}, 800);
			return () => clearTimeout(t);
		}
	});

	function goNext() {
		if (step >= 5) return;
		direction = 'forward';
		step++;
	}

	function goBack() {
		if (step <= 1) return;
		direction = 'backward';
		step--;
	}

	async function validateDeepSeek() {
		if (!deepseekKey.trim()) {
			keyError = 'DeepSeek key is required';
			return false;
		}
		deepseekSaving = true;
		keyError = '';
		try {
			const valRes = await fetch('/api/validate-key', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ provider: 'deepseek', api_key: deepseekKey })
			});
			const valData = await valRes.json();
			if (!valData.valid) {
				keyError = valData.error || 'Invalid DeepSeek key';
				deepseekSaving = false;
				return false;
			}

			const saveRes = await fetch('/api/settings', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					provider: 'deepseek',
					api_key: deepseekKey,
					mode: 'fast'
				})
			});
			if (!saveRes.ok) {
				keyError = 'Failed to save DeepSeek key';
				deepseekSaving = false;
				return false;
			}

			deepseekValid = true;
			await refreshHealthStatus();
			return true;
		} catch {
			keyError = 'Network error — is the server running?';
			return false;
		} finally {
			deepseekSaving = false;
		}
	}

	async function validateGroq() {
		if (!groqKey.trim()) {
			keyError = 'Groq key is required';
			return false;
		}
		groqSaving = true;
		keyError = '';
		try {
			const valRes = await fetch('/api/validate-key', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ provider: 'groq', api_key: groqKey })
			});
			const valData = await valRes.json();
			if (!valData.valid) {
				keyError = valData.error || 'Invalid Groq key';
				groqSaving = false;
				return false;
			}

			const saveRes = await fetch('/api/settings', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					provider: 'groq',
					api_key: groqKey
				})
			});
			if (!saveRes.ok) {
				keyError = 'Failed to save Groq key';
				groqSaving = false;
				return false;
			}

			groqValid = true;
			await refreshHealthStatus();
			return true;
		} catch {
			keyError = 'Network error — is the server running?';
			return false;
		} finally {
			groqSaving = false;
		}
	}

	function handleFinish() {
		guideDone = true;
		onComplete();
	}

	// Step indicators
	let steps = [1, 2, 3, 4, 5];
	let stepLabels = ['Welcome', 'DeepSeek', 'Groq', 'Setup', 'Ready'];

	function isStepComplete(s: number): boolean {
		if (s === 1) return true;
		if (s === 2) return deepseekValid;
		if (s === 3) return groqValid;
		if (s === 4) return $daemonReady;
		if (s === 5) return guideDone;
		return false;
	}

	function isStepActive(s: number): boolean {
		return s === step;
	}
</script>

<div class="flex h-full items-center justify-center p-6 overflow-y-auto">
	<div class="w-full max-w-lg space-y-6 py-4">
		<!-- Step dots indicator -->
		<div class="flex items-center justify-center gap-3">
			{#each steps as s, i}
				<button
					onclick={() => { if (s < step) { direction = 'backward'; step = s; } }}
					disabled={s > step}
					class="guide-dot flex items-center gap-0 {s > step ? 'cursor-default' : 'cursor-pointer'}"
				>
					<div
						class="flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold
							{isStepActive(s)
								? 'bg-[var(--brand)] text-white scale-110'
								: isStepComplete(s)
									? 'bg-[var(--success)]/20 text-[var(--success)] border border-[var(--success)]/40'
									: 'bg-[var(--bg-base)] text-[var(--text-dim)] border border-[var(--border)]'}"
					>
						{#if isStepComplete(s) && !isStepActive(s)}
							<svg class="h-3.5 w-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
								<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7" />
							</svg>
						{:else}
							{s}
						{/if}
					</div>
					{#if i < steps.length - 1}
						<div
							class="h-px w-6 transition-fluid
								{isStepComplete(s) ? 'bg-[var(--success)]/40' : 'bg-[var(--border)]'}"
						></div>
					{/if}
				</button>
			{/each}
		</div>

		<!-- Slide container -->
		<div class="guide-slide">
			{#key step}
				<div
					in:fly={{ x: direction === 'forward' ? 40 : -40, duration: 200 }}
					out:fly={{ x: direction === 'forward' ? -40 : 40, duration: 150 }}
				>
					<!-- Step 1: Welcome -->
					{#if step === 1}
						<div class="space-y-6">
							<div class="text-center">
								<h1 class="text-3xl font-bold text-[var(--text-primary)]">Welcome to Pragma</h1>
								<p class="mt-3 text-[var(--text-muted)]">
									Describe any backend app in plain English — Pragma builds a complete, runnable codebase.
									<span class="text-[var(--brand-light)] font-medium">~$0.03 per project.</span>
								</p>
							</div>

							<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] px-5 py-4 text-sm text-[var(--text-muted)] space-y-1">
								<p class="font-medium text-[var(--text-primary)] mb-2">You'll get:</p>
								<p>✓ A complete backend API (REST endpoints, database models)</p>
								<p>✓ Docker setup so you can run it immediately</p>
								<p>✓ Tests, README, and clean code</p>
								<p class="text-[var(--text-dim)] text-xs mt-2">Note: Generates backend APIs. Use the Next.js profile for a React frontend too.</p>
							</div>

							<div class="rounded-xl border border-[var(--accent)]/30 bg-[var(--accent)]/5 px-5 py-4 text-sm">
								<p class="font-medium text-[var(--accent)] mb-1">You'll need two things:</p>
								<ol class="space-y-1 text-[var(--text-muted)]">
									<li class="flex gap-2"><span class="font-semibold text-[var(--accent)]">1.</span> A <strong class="text-[var(--text-primary)]">DeepSeek</strong> API key (~$0.03/project, pay-as-you-go)</li>
									<li class="flex gap-2"><span class="font-semibold text-[var(--accent)]">2.</span> A free <strong class="text-[var(--text-primary)]">Groq</strong> API key (no credit card needed)</li>
								</ol>
							</div>

							<button
								onclick={goNext}
								class="w-full rounded-xl bg-[var(--brand)] px-6 py-3 text-base font-semibold text-white transition-fluid hover:brightness-110"
							>
								Get Started →
							</button>
						</div>

					<!-- Step 2: DeepSeek Key -->
					{:else if step === 2}
						<div class="space-y-5">
							<div class="text-center">
								<h2 class="text-2xl font-bold text-[var(--text-primary)]">Add your DeepSeek key</h2>
								<p class="mt-2 text-sm text-[var(--text-muted)]">
									DeepSeek powers code generation. <strong class="text-[var(--text-primary)]">Required</strong> to use Pragma.
								</p>
							</div>

							<div class="rounded-xl border-2 border-[var(--brand)] bg-[var(--bg-raised)] p-6 space-y-4">
								<div class="rounded-lg bg-[var(--bg-base)] border border-[var(--border)] px-3 py-2 text-xs text-[var(--text-muted)] space-y-0.5">
									<p>• ~$0.03 per project (about 3 cents)</p>
									<p>• Minimum credit top-up: ~$2 (covers ~60 projects)</p>
									<p>• Pay-as-you-go, no subscription</p>
								</div>

								<div class="space-y-2">
									<input
										type="password"
										bind:value={deepseekKey}
										placeholder="sk-..."
										disabled={deepseekValid}
										class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none focus:ring-1 focus:ring-[var(--brand)] disabled:opacity-60"
									/>
									{#if deepseekValid}
										<p class="text-xs text-[var(--success)]">✓ Key saved and validated</p>
									{/if}
								</div>

								<details class="group">
									<summary class="cursor-pointer text-xs text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]">
										How to get a key (takes 2 minutes)
									</summary>
									<div class="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4 text-sm text-[var(--text-muted)]">
										<ol class="space-y-2 list-none">
											<li class="flex gap-2"><span class="font-bold text-[var(--brand-light)]">1.</span> Go to <a href="https://platform.deepseek.com" target="_blank" rel="noopener noreferrer" class="text-[var(--brand-light)] underline underline-offset-2">platform.deepseek.com</a></li>
											<li class="flex gap-2"><span class="font-bold text-[var(--brand-light)]">2.</span> Sign up (takes 30 seconds)</li>
											<li class="flex gap-2"><span class="font-bold text-[var(--brand-light)]">3.</span> Go to "Top Up" → add $2 credit (minimum)</li>
											<li class="flex gap-2"><span class="font-bold text-[var(--brand-light)]">4.</span> Go to API Keys → Create key</li>
											<li class="flex gap-2"><span class="font-bold text-[var(--brand-light)]">5.</span> Copy the key (starts with sk-) and paste above</li>
										</ol>
									</div>
								</details>
							</div>

							{#if keyError}
								<p class="text-sm text-red-400">{keyError}</p>
							{/if}

							<div class="flex gap-3">
								<button
									onclick={goBack}
									class="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]"
								>
									← Back
								</button>
								{#if deepseekValid}
									<button
										onclick={goNext}
										class="flex-1 rounded-lg bg-[var(--brand)] px-4 py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110"
									>
										Next →
									</button>
								{:else}
									<button
										onclick={validateDeepSeek}
										disabled={deepseekSaving || !deepseekKey.trim()}
										class="flex-1 rounded-lg bg-[var(--brand)] px-4 py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
									>
										{deepseekSaving ? 'Validating…' : 'Save & Continue →'}
									</button>
								{/if}
							</div>
						</div>

					<!-- Step 3: Groq Key -->
					{:else if step === 3}
						<div class="space-y-5">
							<div class="text-center">
								<h2 class="text-2xl font-bold text-[var(--text-primary)]">Add your free Groq key</h2>
								<p class="mt-2 text-sm text-[var(--text-muted)]">
									Groq speeds up chat responses and enables <strong class="text-[var(--text-primary)]">image analysis</strong> (upload mockups, diagrams, or screenshots).
									<span class="text-[var(--brand-light)] font-medium">Completely free — no credit card needed.</span>
								</p>
							</div>

							<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-6 space-y-4">
								<div class="space-y-2">
									<input
										type="password"
										bind:value={groqKey}
										placeholder="gsk_..."
										disabled={groqValid}
										class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none focus:ring-1 focus:ring-[var(--brand)] disabled:opacity-60"
									/>
									{#if groqValid}
										<p class="text-xs text-[var(--success)]">✓ Key saved and validated</p>
									{/if}
								</div>

								<details class="group">
									<summary class="cursor-pointer text-xs text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]">
										How to get a free Groq key
									</summary>
									<div class="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4 text-sm text-[var(--text-muted)]">
										<ol class="space-y-2 list-none">
											<li class="flex gap-2"><span class="font-bold text-green-400">1.</span> Go to <a href="https://console.groq.com" target="_blank" rel="noopener noreferrer" class="text-green-400 underline underline-offset-2">console.groq.com</a></li>
											<li class="flex gap-2"><span class="font-bold text-green-400">2.</span> Sign up with Google or GitHub (no credit card needed)</li>
											<li class="flex gap-2"><span class="font-bold text-green-400">3.</span> Go to API Keys in the left sidebar</li>
											<li class="flex gap-2"><span class="font-bold text-green-400">4.</span> Click Create API Key</li>
											<li class="flex gap-2"><span class="font-bold text-green-400">5.</span> Copy and paste above</li>
										</ol>
									</div>
								</details>
							</div>

							{#if keyError}
								<p class="text-sm text-red-400">{keyError}</p>
							{/if}

							<div class="flex gap-3">
								<button
									onclick={goBack}
									class="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]"
								>
									← Back
								</button>
								{#if groqValid}
									<button
										onclick={goNext}
										class="flex-1 rounded-lg bg-[var(--brand)] px-4 py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110"
									>
										Next →
									</button>
								{:else}
									<button
										onclick={validateGroq}
										disabled={groqSaving || !groqKey.trim()}
										class="flex-1 rounded-lg bg-[var(--brand)] px-4 py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
									>
										{groqSaving ? 'Validating…' : 'Save & Continue →'}
									</button>
								{/if}
							</div>
						</div>

					<!-- Step 4: Setup Check -->
					{:else if step === 4}
						<div class="space-y-5">
							<div class="text-center">
								<h2 class="text-2xl font-bold text-[var(--text-primary)]">Checking your setup</h2>
								<p class="mt-2 text-sm text-[var(--text-muted)]">
									Making sure everything is ready to go.
								</p>
							</div>

							<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-6 space-y-4">
								{#if $healthData}
									{#each Object.entries($healthData.checks) as [key, check]}
										{#if key === 'python' || key === 'daemon'}
											<div class="flex items-start gap-3">
												<span class="text-base leading-tight mt-0.5">
													{check.ok ? '✅' : '⏳'}
												</span>
												<div class="flex-1 min-w-0">
													<p class="text-sm font-medium text-[var(--text-primary)] capitalize">
														{key === 'daemon' ? 'Daemon (Python service)' : 'Python'}
													</p>
													<p class="text-xs text-[var(--text-muted)] mt-0.5">{check.message}</p>
													{#if !check.ok && key === 'python'}
														<a
															href="https://www.python.org/downloads/"
															target="_blank"
															rel="noopener noreferrer"
															class="mt-1 inline-block text-xs text-[var(--brand-light)] underline underline-offset-2"
														>Install Python 3.11+</a>
													{/if}
													{#if !check.ok && key === 'daemon'}
														<p class="mt-1 text-xs text-[var(--accent)]">
															Run <code class="font-mono bg-[var(--bg-base)] px-1 rounded">pragma setup</code> in your terminal, or
															run <code class="font-mono bg-[var(--bg-base)] px-1 rounded">pragma setup</code> from the project root.
														</p>
													{/if}
												</div>
											</div>
										{/if}
									{/each}
								{:else}
									<div class="flex items-center justify-center py-6">
										<div class="h-5 w-5 animate-spin rounded-full border-2 border-[var(--brand)] border-t-transparent"></div>
									</div>
								{/if}

								{#if $daemonReady}
									<div class="rounded-lg bg-[var(--success)]/10 border border-[var(--success)]/30 px-4 py-3 text-sm text-[var(--success)] text-center">
										✓ Daemon is running — you're all set!
									</div>
								{/if}
							</div>

							<div class="flex gap-3">
								<button
									onclick={goBack}
									class="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]"
								>
									← Back
								</button>
								{#if $daemonReady}
									<button
										onclick={goNext}
										class="flex-1 rounded-lg bg-[var(--brand)] px-4 py-3 text-sm font-semibold text-white transition-fluid hover:brightness-110"
									>
										Next →
									</button>
								{:else}
									<button
										onclick={() => refreshHealthStatus()}
										disabled={$isHealthChecking}
										class="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] disabled:opacity-50 disabled:cursor-not-allowed"
									>
										{#if $isHealthChecking}
											<span class="flex items-center justify-center gap-2">
												<svg class="h-4 w-4 animate-spin" fill="none" stroke="currentColor" viewBox="0 0 24 24">
													<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
												</svg>
												Checking…
											</span>
										{:else}
											↻ Check Again
										{/if}
									</button>
								{/if}
							</div>
						</div>

					<!-- Step 5: Ready -->
					{:else if step === 5}
						<div class="space-y-6">
							<div class="text-center">
								<div class="mb-3 text-5xl">🎉</div>
								<h2 class="text-2xl font-bold text-[var(--text-primary)]">You're all set!</h2>
								<p class="mt-2 text-sm text-[var(--text-muted)]">
									Everything is ready. Describe your project and Pragma will build it.
								</p>
							</div>

							<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] px-5 py-4 text-sm space-y-2">
								<div class="flex items-center gap-2">
									<span class="text-[var(--success)]">✓</span>
									<span class="text-[var(--text-primary)]">DeepSeek key configured</span>
								</div>
								<div class="flex items-center gap-2">
									<span class="text-[var(--success)]">✓</span>
									<span class="text-[var(--text-primary)]">Groq key configured</span>
								</div>
								<div class="flex items-center gap-2">
									<span class="text-[var(--success)]">✓</span>
									<span class="text-[var(--text-primary)]">Daemon running</span>
								</div>
								{#if $healthData?.checks?.docker?.ok}
									<div class="flex items-center gap-2">
										<span class="text-[var(--success)]">✓</span>
										<span class="text-[var(--text-primary)]">Docker available</span>
									</div>
								{/if}
							</div>

							<button
								onclick={handleFinish}
								class="w-full rounded-xl bg-[var(--brand)] px-6 py-3.5 text-base font-semibold text-white transition-fluid hover:brightness-110"
							>
								Start Building →
							</button>
							<p class="text-center text-xs text-[var(--text-dim)]">You can always update settings in the sidebar later.</p>
						</div>
					{/if}
				</div>
			{/key}
		</div>
	</div>
</div>
