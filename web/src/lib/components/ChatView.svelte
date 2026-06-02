<script lang="ts">
	import { messages, sendMessage, phase, interviewDone, connected, reconnectFailed, connect } from '$lib/stores/ws';
	import { tick } from 'svelte';

	let inputValue = $state('');
	let chatContainer: HTMLDivElement;
	let inputEl: HTMLTextAreaElement | undefined = $state();
	let sending = $state(false);

	const FIRST_MSG_MIN = 20;
	let isFirstMessage = $derived($messages.length === 0);
	let trimmed = $derived(inputValue.trim());
	let canSend = $derived(
		trimmed.length > 0 && (!isFirstMessage || trimmed.length >= FIRST_MSG_MIN)
	);
	let firstMsgCharsLeft = $derived(
		isFirstMessage ? Math.max(0, FIRST_MSG_MIN - trimmed.length) : 0
	);

	// Auto-scroll to bottom on new message (smooth, not jarring)
	$effect(() => {
		if ($messages.length && chatContainer) {
			tick().then(() => {
				if (chatContainer) {
					chatContainer.scrollTo({ top: chatContainer.scrollHeight, behavior: 'smooth' });
				}
			});
		}
	});

	function handleSend() {
		const text = inputValue.trim();
		if (!text || sending || !canSend) return;
		sending = true;
		inputValue = '';
		sendMessage(text);
		// Re-enable after a short debounce (prevents double-send)
		setTimeout(() => (sending = false), 300);
		// Refocus input
		tick().then(() => inputEl?.focus());
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		}
	}

	// Auto-resize textarea
	function autoResize(el: HTMLTextAreaElement) {
		el.style.height = 'auto';
		el.style.height = Math.min(el.scrollHeight, 160) + 'px';
	}
</script>

<div class="flex h-full flex-col">
	<!-- Messages area -->
	<div bind:this={chatContainer} class="flex-1 overflow-y-auto px-4 py-6 md:px-8">
		<div class="mx-auto max-w-2xl space-y-4">
			{#if $messages.length === 0}
				<!-- Empty state -->
				<div class="flex flex-col items-center justify-center pt-20 text-center">
					<div class="mb-4 text-4xl">✨</div>
					<h1 class="mb-2 text-2xl font-semibold text-[var(--text-primary)]">What do you want to build?</h1>
					<p class="max-w-md text-[var(--text-muted)]">
						Describe your project in plain English — like you'd explain it to a friend. Pragma will ask a few follow-up questions, then build it.
					</p>
				</div>
			{/if}

			{#each $messages as msg, i (msg.timestamp + i)}
				<div class="enter-from-below" style="animation-delay: {Math.min(i * 30, 150)}ms">
					{#if msg.role === 'user'}
						<div class="flex justify-end">
							<div class="max-w-[80%] rounded-2xl rounded-br-md bg-[var(--brand)] px-4 py-2.5 text-sm text-white shadow-md">
								{msg.content}
							</div>
						</div>
					{:else if msg.role === 'assistant'}
						<div class="flex justify-start">
							<div class="max-w-[80%] rounded-2xl rounded-bl-md bg-[var(--bg-raised)] px-4 py-2.5 text-sm text-[var(--text-primary)] shadow-md border border-[var(--border)]">
								{msg.content}
							</div>
						</div>
					{:else}
						<!-- Error message -->
						<div class="flex justify-center">
							<div class="rounded-lg bg-[var(--error)]/10 border border-[var(--error)]/30 px-4 py-2 text-sm text-[var(--error)]">
								{msg.content}
							</div>
						</div>
					{/if}
				</div>
			{/each}

			{#if sending || ($phase === 'interview' && $messages.length > 0 && $messages[$messages.length - 1]?.role === 'user')}
				{#if !$connected}
					<!-- Reconnect state -->
					<div class="flex justify-start enter-from-below">
						<div class="rounded-2xl rounded-bl-md bg-[var(--bg-raised)] px-4 py-3 border border-[var(--border)]">
							{#if $reconnectFailed}
								<button
									onclick={() => connect()}
									class="flex items-center gap-2 text-sm text-[var(--warning)] hover:underline cursor-pointer"
								>
									<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
									</svg>
									Connection lost. Click to retry.
								</button>
							{:else}
								<div class="flex items-center gap-2 text-sm text-[var(--text-muted)]">
									<svg class="h-4 w-4 animate-spin" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
									</svg>
									Connection lost - reconnecting...
								</div>
							{/if}
						</div>
					</div>
				{:else}
					<!-- Typing indicator -->
					<div class="flex justify-start enter-from-below">
						<div class="rounded-2xl rounded-bl-md bg-[var(--bg-raised)] px-4 py-3 border border-[var(--border)]">
							<div class="flex gap-1">
								<span class="h-2 w-2 rounded-full bg-[var(--text-dim)] animate-[pulse-soft_1.2s_infinite_0ms]"></span>
								<span class="h-2 w-2 rounded-full bg-[var(--text-dim)] animate-[pulse-soft_1.2s_infinite_200ms]"></span>
								<span class="h-2 w-2 rounded-full bg-[var(--text-dim)] animate-[pulse-soft_1.2s_infinite_400ms]"></span>
							</div>
						</div>
					</div>
				{/if}
			{/if}
		</div>
	</div>

	<!-- Input area (pinned to bottom) -->
	{#if !$interviewDone}
		<div class="border-t border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 md:px-8">
			<div class="mx-auto flex max-w-2xl items-end gap-3">
				<textarea
					bind:this={inputEl}
					bind:value={inputValue}
					onkeydown={handleKeydown}
					oninput={(e) => autoResize(e.currentTarget)}
					placeholder="Describe what you want to build..."
					rows="1"
					class="flex-1 resize-none rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder-[var(--text-dim)] transition-all duration-150 focus:border-[var(--border-focus)] focus:outline-none"
				></textarea>
				<button
					onclick={handleSend}
					disabled={!canSend || sending}
					aria-label="Send message"
					class="flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--brand)] text-white transition-fluid hover:scale-105 hover:brightness-110 active:scale-95 disabled:opacity-40 disabled:hover:scale-100"
				>
					<svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14m-7-7 7 7-7 7" />
					</svg>
				</button>
			</div>
			<p class="mx-auto mt-2 max-w-2xl text-center text-xs text-[var(--text-dim)]">
				{#if isFirstMessage && trimmed.length > 0 && firstMsgCharsLeft > 0}
					{firstMsgCharsLeft} more character{firstMsgCharsLeft === 1 ? '' : 's'} needed to describe your project
				{:else}
					Press Enter to send · Shift+Enter for new line
				{/if}
			</p>
		</div>
	{/if}
</div>
