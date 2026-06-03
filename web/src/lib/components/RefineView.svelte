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
				// Reset the refinement stores so the next extend uses the new state
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
</script>

<div class="refine-container">
	<div class="refine-header">
		<h2>Refine Your Project</h2>
		<p class="subtitle">Chat with Pragma to add features, fix issues, or tweak your app.</p>
	</div>

	{#if isAnalyzing}
		<div class="analyzing-indicator" role="status" aria-live="polite">
			<div class="spinner"></div>
			<span>Analyzing impact of your changes...</span>
		</div>
	{/if}

	{#if isApplying}
		<div class="applying-indicator" role="status" aria-live="polite">
			<div class="spinner"></div>
			<span>Applying your approved changes...</span>
		</div>
	{/if}

	{#if impact && delta}
		<div class="impact-card" role="region" aria-label="Change impact analysis">
			<div class="impact-header">
				<h3>Proposed Changes</h3>
				<button class="dismiss-btn" onclick={dismissAnalysis} aria-label="Dismiss analysis">✕</button>
			</div>
			
			<div class="impact-summary">
				<p>{impact.impact_summary}</p>
			</div>

			{#if (impact.affected_files || []).length > 0}
				<div class="impact-section">
					<strong>Modified files:</strong>
					<ul>
						{#each impact.affected_files || [] as file}
							<li><code>{file}</code></li>
						{/each}
					</ul>
				</div>
			{/if}

			{#if (impact.new_files || []).length > 0}
				<div class="impact-section">
					<strong>New files:</strong>
					<ul>
						{#each impact.new_files || [] as file}
							<li><code>{file}</code></li>
						{/each}
					</ul>
				</div>
			{/if}

			{#if (impact.risk_reasons || []).length > 0}
				<div class="impact-section risk">
					<strong>⚠️ Potential risks:</strong>
					<ul>
						{#each impact.risk_reasons || [] as reason}
							<li>{reason}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<div class="impact-actions">
				<button class="approve-btn" onclick={approveChanges} disabled={isApplying}>
					{isApplying ? 'Applying...' : '✅ Approve & Apply'}
				</button>
				<button class="cancel-btn" onclick={dismissAnalysis} disabled={isApplying}>
					Cancel
				</button>
			</div>
		</div>
	{/if}

	<div class="conversation" role="log" aria-live="polite" aria-label="Conversation history">
		{#each conversationHistory as msg}
			<div class="message {msg.role}">
				<span class="role-label">{msg.role === 'user' ? 'You' : 'Pragma'}</span>
				<div class="content">{@html sanitizeHtml(msg.content.replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>').replace(/\n/g, '<br>'))}</div>
			</div>
		{/each}
	</div>

	<form class="chat-input" onsubmit={(e) => { e.preventDefault(); sendMessage(); }}>
		<input 
			type="text" 
			bind:value={userMessage} 
			placeholder="e.g. Add a dark mode toggle to the settings page..." 
			disabled={isAnalyzing || isApplying}
			aria-label="Your change request"
		/>
		<button type="submit" disabled={!userMessage.trim() || isAnalyzing || isApplying}>
			{isAnalyzing ? '...' : 'Send'}
		</button>
	</form>
</div>

<style>
	.refine-container {
		display: flex;
		flex-direction: column;
		height: 100%;
		max-width: 800px;
		margin: 0 auto;
		padding: 1rem;
	}

	.refine-header {
		text-align: center;
		margin-bottom: 1.5rem;
	}

	.refine-header h2 {
		font-size: 1.5rem;
		font-weight: 600;
		color: #1a1a2e;
		margin: 0;
	}

	.subtitle {
		color: #6b7280;
		font-size: 0.875rem;
		margin: 0.5rem 0 0;
	}

	.analyzing-indicator, .applying-indicator {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 1rem;
		background: #f3f4f6;
		border-radius: 0.5rem;
		margin-bottom: 1rem;
		color: #4b5563;
		font-size: 0.875rem;
	}

	.spinner {
		width: 1rem;
		height: 1rem;
		border: 2px solid #d1d5db;
		border-top-color: #3b82f6;
		border-radius: 50%;
		animation: spin 0.6s linear infinite;
	}

	@keyframes spin {
		to { transform: rotate(360deg); }
	}

	.impact-card {
		background: #fff;
		border: 1px solid #e5e7eb;
		border-radius: 0.75rem;
		padding: 1.25rem;
		margin-bottom: 1rem;
		box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
	}

	.impact-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 1rem;
	}

	.impact-header h3 {
		font-size: 1rem;
		font-weight: 600;
		margin: 0;
		color: #1a1a2e;
	}

	.dismiss-btn {
		background: none;
		border: none;
		cursor: pointer;
		font-size: 1rem;
		color: #9ca3af;
		padding: 0.25rem;
	}

	.dismiss-btn:hover {
		color: #6b7280;
	}

	.impact-summary {
		font-size: 0.9375rem;
		color: #374151;
		margin-bottom: 1rem;
		line-height: 1.5;
	}

	.impact-section {
		margin-bottom: 0.75rem;
	}

	.impact-section strong {
		font-size: 0.8125rem;
		color: #6b7280;
	}

	.impact-section ul {
		margin: 0.5rem 0 0;
		padding-left: 1.25rem;
	}

	.impact-section li {
		font-size: 0.875rem;
		color: #374151;
		margin-bottom: 0.25rem;
	}

	.impact-section code {
		background: #f3f4f6;
		padding: 0.125rem 0.375rem;
		border-radius: 0.25rem;
		font-size: 0.8125rem;
	}

	.impact-section.risk {
		background: #fef3c7;
		padding: 0.75rem;
		border-radius: 0.5rem;
		margin-top: 1rem;
	}

	.impact-section.risk strong {
		color: #92400e;
	}

	.impact-section.risk li {
		color: #78350f;
	}

	.impact-actions {
		display: flex;
		gap: 0.75rem;
		margin-top: 1rem;
		padding-top: 1rem;
		border-top: 1px solid #f3f4f6;
	}

	.approve-btn {
		flex: 1;
		padding: 0.75rem 1rem;
		background: #10b981;
		color: white;
		border: none;
		border-radius: 0.5rem;
		font-size: 0.9375rem;
		font-weight: 500;
		cursor: pointer;
		transition: background 0.15s;
	}

	.approve-btn:hover:not(:disabled) {
		background: #059669;
	}

	.approve-btn:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.cancel-btn {
		padding: 0.75rem 1rem;
		background: #f3f4f6;
		color: #4b5563;
		border: none;
		border-radius: 0.5rem;
		font-size: 0.9375rem;
		cursor: pointer;
		transition: background 0.15s;
	}

	.cancel-btn:hover:not(:disabled) {
		background: #e5e7eb;
	}

	.conversation {
		flex: 1;
		overflow-y: auto;
		padding: 0.5rem 0;
	}

	.message {
		display: flex;
		flex-direction: column;
		margin-bottom: 1rem;
		padding: 0.75rem 1rem;
		border-radius: 0.5rem;
	}

	.message.user {
		background: #dbeafe;
		align-self: flex-end;
		margin-left: 2rem;
	}

	.message.assistant {
		background: #f3f4f6;
		align-self: flex-start;
		margin-right: 2rem;
	}

	.role-label {
		font-size: 0.75rem;
		font-weight: 600;
		color: #6b7280;
		margin-bottom: 0.25rem;
		text-transform: uppercase;
	}

	.message.user .role-label {
		color: #2563eb;
	}

	.message.assistant .role-label {
		color: #6b7280;
	}

	.content {
		font-size: 0.9375rem;
		line-height: 1.5;
		color: #374151;
	}

	.chat-input {
		display: flex;
		gap: 0.75rem;
		margin-top: 1rem;
		padding-top: 1rem;
		border-top: 1px solid #e5e7eb;
	}

	.chat-input input {
		flex: 1;
		padding: 0.75rem 1rem;
		border: 1px solid #e5e7eb;
		border-radius: 0.5rem;
		font-size: 0.9375rem;
		outline: none;
		transition: border-color 0.15s;
	}

	.chat-input input:focus {
		border-color: #3b82f6;
	}

	.chat-input button {
		padding: 0.75rem 1.5rem;
		background: #3b82f6;
		color: white;
		border: none;
		border-radius: 0.5rem;
		font-size: 0.9375rem;
		font-weight: 500;
		cursor: pointer;
		transition: background 0.15s;
	}

	.chat-input button:hover:not(:disabled) {
		background: #2563eb;
	}

	.chat-input button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
</style>