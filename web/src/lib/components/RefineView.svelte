<script lang="ts">
	import { runResult } from '$lib/stores/ws';
	import { checkpointManifest, checkpointSpec } from '$lib/stores/refine';

	// Basic HTML sanitizer to prevent XSS while preserving formatting
	function sanitizeHtml(html: string): string {
		return html
			.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
			.replace(/<img\b[^<]*(?:(?!>)<[^<]*)*>/gi, '')
			.replace(/<iframe\b[^<]*(?:(?!>)<[^<]*)*>/gi, '')
			.replace(/<object\b[^<]*(?:(?!>)<[^<]*)*>/gi, '')
			.replace(/<embed\b[^<]*(?:(?!>)<[^<]*)*>/gi, '')
			.replace(/on\w+\s*=/gi, 'data-removed=')
			.replace(/javascript:/gi, '')
			.replace(/data:/gi, '');
	}

	let userMessage = $state('');
	let isAnalyzing = $state(false);
	let isApplying = $state(false);
	let impact = $state<any>(null);
	let delta = $state<any>(null);
	let error = $state('');
	let conversationHistory = $state<Array<{role: string, content: string}>>([]);

	async function sendMessage() {
		if (!userMessage.trim() || isAnalyzing || isApplying) return;
		
		const message = userMessage.trim();
		userMessage = '';
		conversationHistory.push({ role: 'user', content: message });
		
		isAnalyzing = true;
		error = '';
		impact = null;
		delta = null;

		try {
			const res = await fetch('/api/extend-project', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					checkpoint_manifest: $checkpointManifest,
					checkpoint_spec: $checkpointSpec,
					new_requirements: message
				})
			});
			const data = await res.json();
			
			if (!res.ok) {
				error = data.error || 'Failed to analyze changes';
				conversationHistory.push({ role: 'assistant', content: `⚠️ ${error}` });
			} else {
				impact = data.impact;
				delta = data.delta;
				
				const riskIcon = impact.risk_level === 'high' ? '🔴' : impact.risk_level === 'medium' ? '🟡' : '🟢';
				const analysisSummary = `${riskIcon} **Impact Analysis**
**Summary:** ${impact.impact_summary}
**Affected files:** ${(impact.affected_files || []).length > 0 ? (impact.affected_files || []).join(', ') : 'None'}
**New files:** ${(impact.new_files || []).length > 0 ? (impact.new_files || []).join(', ') : 'None'}
**Risk:** ${impact.risk_level}
${(impact.risk_reasons || []).length > 0 ? `**Risks:**\n${(impact.risk_reasons || []).map((r: string) => `• ${r}`).join('\n')}` : ''}`;
				
				conversationHistory.push({ role: 'assistant', content: analysisSummary });
			}
		} catch (e) {
			error = 'Network error. Please try again.';
			conversationHistory.push({ role: 'assistant', content: `⚠️ ${error}` });
		} finally {
			isAnalyzing = false;
		}
	}

	async function approveChanges() {
		if (!delta || isApplying) return;
		
		isApplying = true;
		conversationHistory.push({ role: 'assistant', content: '⏳ Applying your changes. This will take a moment...' });

		try {
			const runId = ($runResult?.output_path || '').split(/[/\\]/).pop();
			const res = await fetch('/api/apply-delta', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ run_id: runId, delta_spec: delta })
			});
			const data = await res.json();
			
			if (!res.ok) {
				conversationHistory.push({ role: 'assistant', content: `⚠️ Failed to apply changes: ${data.error || 'Unknown error'}` });
			} else {
				conversationHistory.push({ role: 'assistant', content: '✅ Changes applied successfully! Your project has been updated.' });
				impact = null;
				delta = null;
				checkpointSpec.set(data.updated_spec || {});
			}
		} catch (e) {
			conversationHistory.push({ role: 'assistant', content: '⚠️ Network error while applying changes.' });
		} finally {
			isApplying = false;
		}
	}

	function dismissAnalysis() {
		impact = null;
		delta = null;
		error = '';
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			sendMessage();
		}
	}
</script>

<div class="flex h-full flex-col max-w-3xl mx-auto p-4 md:p-6">
	<!-- Header -->
	<div class="text-center mb-4">
		<h2 class="text-xl font-bold text-[var(--text-primary)]">Refine Your Project</h2>
		<p class="text-sm text-[var(--text-dim)] mt-1">Chat with Pragma to add features, fix issues, or tweak your app.</p>
	</div>

	<!-- Loading indicators -->
	{#if isAnalyzing}
		<div class="flex items-center gap-3 p-3 rounded-lg bg-[var(--bg-base)] border border-[var(--border)] mb-3" role="status" aria-live="polite">
			<div class="h-4 w-4 border-2 border-[var(--border)] border-t-[var(--brand)] rounded-full animate-spin"></div>
			<span class="text-sm text-[var(--text-muted)]">Analyzing impact of your changes...</span>
		</div>
	{/if}

	{#if isApplying}
		<div class="flex items-center gap-3 p-3 rounded-lg bg-[var(--bg-base)] border border-[var(--border)] mb-3" role="status" aria-live="polite">
			<div class="h-4 w-4 border-2 border-[var(--border)] border-t-[var(--accent)] rounded-full animate-spin"></div>
			<span class="text-sm text-[var(--text-muted)]">Applying your approved changes...</span>
		</div>
	{/if}

	<!-- Impact analysis card -->
	{#if impact && delta}
		<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-4 mb-3" role="region" aria-label="Change impact analysis">
			<div class="flex items-center justify-between mb-3">
				<h3 class="text-sm font-semibold text-[var(--text-primary)]">Proposed Changes</h3>
				<button onclick={dismissAnalysis} aria-label="Dismiss analysis" class="rounded p-1 text-[var(--text-dim)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-fluid">
					<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
				</button>
			</div>

			<p class="text-sm text-[var(--text-muted)] mb-3">{impact.impact_summary}</p>

			{#if (impact.affected_files || []).length > 0}
				<div class="mb-2">
					<p class="text-xs font-medium text-[var(--text-dim)] mb-1">Modified files:</p>
					<div class="flex flex-wrap gap-1">
						{#each impact.affected_files || [] as file}
							<code class="text-xs bg-[var(--bg-base)] text-[var(--accent)] px-1.5 py-0.5 rounded">{file}</code>
						{/each}
					</div>
				</div>
			{/if}

			{#if (impact.new_files || []).length > 0}
				<div class="mb-2">
					<p class="text-xs font-medium text-[var(--text-dim)] mb-1">New files:</p>
					<div class="flex flex-wrap gap-1">
						{#each impact.new_files || [] as file}
							<code class="text-xs bg-[var(--bg-base)] text-[var(--success)] px-1.5 py-0.5 rounded">{file}</code>
						{/each}
					</div>
				</div>
			{/if}

			{#if (impact.risk_reasons || []).length > 0}
				<div class="mt-2 p-2.5 rounded-lg bg-[var(--warning)]/10 border border-[var(--warning)]/20">
					<p class="text-xs font-medium text-[var(--warning)] mb-1">Potential risks:</p>
					<ul class="space-y-0.5">
						{#each impact.risk_reasons || [] as reason}
							<li class="text-xs text-[var(--text-muted)]">• {reason}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<div class="flex gap-2 mt-3 pt-3 border-t border-[var(--border)]">
				<button onclick={approveChanges} disabled={isApplying} class="flex-1 rounded-lg bg-[var(--success)] py-2.5 text-sm font-medium text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed">
					{isApplying ? 'Applying...' : '✅ Approve & Apply'}
				</button>
				<button onclick={dismissAnalysis} disabled={isApplying} class="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-2.5 text-sm text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] disabled:opacity-50">
					Cancel
				</button>
			</div>
		</div>
	{/if}

	<!-- Conversation -->
	<div class="flex-1 min-h-0 overflow-y-auto space-y-3 py-2" role="log" aria-live="polite" aria-label="Conversation history">
		{#each conversationHistory as msg}
			<div class="flex flex-col {msg.role === 'user' ? 'items-end ml-8' : 'items-start mr-8'}">
				<span class="text-xs font-semibold uppercase tracking-wider mb-1 {msg.role === 'user' ? 'text-[var(--brand)]' : 'text-[var(--text-dim)]'}">
					{msg.role === 'user' ? 'You' : 'Pragma'}
				</span>
				<div class="rounded-lg px-3 py-2 text-sm leading-relaxed {msg.role === 'user' ? 'bg-[var(--brand)]/15 text-[var(--text-primary)]' : 'bg-[var(--bg-base)] text-[var(--text-muted)]'}">
					{@html sanitizeHtml(msg.content.replace(/\*\*(.*?)\*\*/g, '<strong class="text-[var(--text-primary)]">$1</strong>').replace(/\n/g, '<br>'))}
				</div>
			</div>
		{/each}
	</div>

	<!-- Chat input -->
	<form class="flex gap-2 mt-3 pt-3 border-t border-[var(--border)]" onsubmit={(e) => { e.preventDefault(); sendMessage(); }}>
		<input
			type="text"
			bind:value={userMessage}
			onkeydown={handleKeydown}
			placeholder="e.g. Add a dark mode toggle to the settings page..."
			disabled={isAnalyzing || isApplying}
			aria-label="Your change request"
			class="flex-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none disabled:opacity-50"
		/>
		<button
			type="submit"
			disabled={!userMessage.trim() || isAnalyzing || isApplying}
			class="rounded-lg bg-[var(--brand)] px-4 py-2.5 text-sm font-medium text-white transition-fluid hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed"
		>
			{isAnalyzing ? '...' : 'Send'}
		</button>
	</form>
</div>
