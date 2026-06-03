<script lang="ts">
	import ChatView from '$lib/components/ChatView.svelte';
	import ProjectInput from '$lib/components/ProjectInput.svelte';
	import WorkingView from '$lib/components/WorkingView.svelte';
	import SpecReview from '$lib/components/SpecReview.svelte';
	import DagApproval from '$lib/components/DagApproval.svelte';
	import GeneratingView from '$lib/components/GeneratingView.svelte';
	import CompleteView from '$lib/components/CompleteView.svelte';
	import RefineView from '$lib/components/RefineView.svelte';
	import SetupGuide from '$lib/components/SetupGuide.svelte';
	import PreCompileView from '$lib/components/PreCompileView.svelte';
	import { phase, interviewDone, showPreCompile, errorMsg, resetStores, retryLastAction } from '$lib/stores/ws';
	import { bothKeysReady, daemonReady, healthData, refreshHealthStatus, guideActive } from '$lib/stores/settings';

	let loaded = $state(false);
	let showGuide = $state(false);
	let showErrorDetails = $state(false);
	let errorLogs = $state('');
	let copying = $state(false);

	// WSL detection from health endpoint
	let isWSL = $derived($healthData?.is_wsl ?? false);
	let wsPort = $derived($healthData?.port ?? '3777');

	let displayErrorMsg = $derived.by(() => {
		if (!$errorMsg) return 'Something went wrong. Click Retry or Start Over.';
		const msg = $errorMsg.toLowerCase();
		if (msg.includes('credits exhausted')) return 'Your DeepSeek account ran out of credits... click Retry.';
		if (msg.includes('spec compilation failed')) return "The AI couldn't design your project... click Retry.";
		if (msg.includes('too many failed files')) return 'Too many files failed... Try a simpler description.';
		if (msg.includes('timeout')) return 'The AI took too long... click Retry.';
		return 'Something went wrong. Click Retry or Start Over.';
	});

	async function checkSettings() {
		await refreshHealthStatus();
		// Only show guide if keys aren't already both ready
		if (!$bothKeysReady) {
			showGuide = true;
		}
		loaded = true;
	}

	function handleGuideComplete() {
		showGuide = false;
		// Refresh health to detect daemon startup after key save
		setTimeout(() => refreshHealthStatus(), 2500);
	}

	// Sync showGuide state to the guideActive store for the sidebar
	$effect(() => {
		guideActive.set(showGuide);
	});

	// Re-show guide if user clears both keys while at the main app
	$effect(() => {
		if (loaded && !$bothKeysReady) {
			showGuide = true;
		}
	});

	async function fetchErrorLogs() {
		showErrorDetails = !showErrorDetails;
		if (showErrorDetails && !errorLogs) {
			try {
				const res = await fetch('/api/logs');
				if (res.ok) {
					const text = await res.text();
					errorLogs = text.split('\n').slice(-50).join('\n');
				} else {
					errorLogs = 'Failed to fetch logs.';
				}
			} catch {
				errorLogs = 'Could not connect to server.';
			}
		}
	}

	async function copyLogs() {
		try {
			await navigator.clipboard.writeText(errorLogs);
			copying = true;
			setTimeout(() => (copying = false), 2000);
		} catch {}
	}

	$effect(() => {
		checkSettings();
	});
</script>

<div class="flex h-full flex-col">
	<!-- WSL banner -->
	{#if isWSL && loaded}
		<div class="flex items-center gap-2 border-b border-[var(--brand)]/20 bg-[var(--brand)]/8 px-4 py-2 text-sm text-[var(--text-muted)]">
			<span class="text-base">&#x1F4BB;</span>
			<span><span class="font-medium text-[var(--brand-light)]">Running on WSL</span> — open <a href="http://localhost:{wsPort}" target="_blank" class="font-mono text-[var(--accent)] underline underline-offset-2">http://localhost:{wsPort}</a> in your Windows browser (Edge, Chrome, or Firefox). You can also create good frontends with the Next.js profile.</span>
		</div>
	{/if}

	{#if !loaded}
		<!-- Loading state -->
		<div class="flex flex-1 items-center justify-center">
			<div class="h-8 w-8 animate-spin rounded-full border-2 border-[var(--brand)] border-t-transparent"></div>
		</div>
	{:else if showGuide}
		<div class="flex-1 enter-from-below">
			<SetupGuide onComplete={handleGuideComplete} />
		</div>
	{:else if $phase === 'idle'}
		<div class="flex-1 enter-from-below">
			{#if !$daemonReady}
				<div class="mx-auto mt-4 max-w-lg rounded-lg border border-[var(--accent)]/30 bg-[var(--accent)]/5 px-4 py-3 text-sm text-[var(--text-muted)]">
					<span class="font-medium text-[var(--accent)]">Daemon not running</span> — some features are unavailable.
					Run <code class="font-mono bg-[var(--bg-base)] px-1 rounded">pragma setup</code> in your terminal, or use
					<code class="font-mono bg-[var(--bg-base)] px-1 rounded">pragma setup</code> from the project root.
				</div>
			{/if}
			<ProjectInput />
		</div>
	{:else if $phase === 'ideation' && $interviewDone && $showPreCompile}
		<div class="flex-1 enter-from-below">
			<PreCompileView />
		</div>
	{:else if $phase === 'ideation' || $phase === 'interview'}
		<div class="flex-1 enter-from-below">
			<ChatView />
		</div>
	{:else if $phase === 'researching' || $phase === 'compiling_spec'}
		<div class="flex-1 enter-from-below">
			<WorkingView />
		</div>
	{:else if $phase === 'spec_review'}
		<div class="flex-1 enter-from-below">
			<SpecReview />
		</div>
	{:else if $phase === 'dag_review'}
		<div class="flex-1 enter-from-below">
			<DagApproval />
		</div>
	{:else if $phase === 'generating'}
		<div class="flex-1 enter-from-below">
			<GeneratingView />
		</div>
	{:else if $phase === 'complete'}
		<div class="flex-1 enter-from-below">
			<CompleteView />
		</div>
	{:else if $phase === 'error'}
		<div class="flex flex-1 flex-col items-center justify-center px-4 overflow-y-auto py-8">
			<div class="w-full max-w-lg rounded-2xl border border-[var(--error)]/30 bg-[var(--bg-raised)] p-8 text-center my-auto">
				<div class="mb-3 text-4xl">⚠️</div>
				<h2 class="mb-2 text-xl font-bold text-[var(--text-primary)]">Something went wrong</h2>
				<p class="mb-4 text-sm text-[var(--text-muted)]">{displayErrorMsg}</p>
				<div class="flex flex-wrap justify-center gap-3">
					<button
						onclick={fetchErrorLogs}
						class="rounded-lg border border-[var(--border)] px-4 py-2 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)]"
					>
						{showErrorDetails ? 'Hide details' : 'View details'}
					</button>
					<button
						onclick={() => { retryLastAction(); }}
						class="rounded-lg bg-[var(--bg-base)] border border-[var(--border)] px-4 py-2 text-sm font-medium text-[var(--text-primary)] transition-fluid hover:bg-[var(--bg-hover)]"
					>
						Retry
					</button>
					<button
						onclick={() => { resetStores(); }}
						class="rounded-lg bg-[var(--brand)] px-4 py-2 text-sm font-medium text-white transition-fluid hover:brightness-110"
					>
						Start New Project
					</button>
				</div>
				{#if showErrorDetails}
					<div class="mt-4 text-left">
						<div class="flex items-center justify-between mb-2">
							<span class="text-xs text-[var(--text-dim)]">Log output</span>
							<button
								onclick={copyLogs}
								class="text-xs text-[var(--brand-light)] hover:underline"
							>
								{copying ? 'Copied!' : 'Copy to clipboard'}
							</button>
						</div>
						<pre class="max-h-60 overflow-auto rounded-lg bg-[var(--bg-base)] border border-[var(--border)] p-3 text-xs text-[var(--text-muted)] whitespace-pre-wrap">{errorLogs || 'Loading...'}</pre>
					</div>
				{/if}
			</div>
		</div>
	{:else}
		<div class="flex-1 enter-from-below">
			<ChatView />
		</div>
	{/if}
</div>
		<ChatView />
		</div>
	{/if}
</div>
