<script lang="ts">
	import { startInterview } from '$lib/stores/ws';
	import { healthData } from '$lib/stores/settings';

	const MIN_LENGTH = 20;

	let description = $state('');
	let showExamples = $state(false);
	let showImageUpload = $state(false);

	// Pragma auto-selects the best build profile from your description
	// (e.g. Python/FastAPI for backends, Next.js for full-stack, Go/Fiber for Go projects).
	// The chosen profile is shown before building so you know what's being used.

	let trimmed  = $derived(description.trim());
	let charsLeft = $derived(Math.max(0, MIN_LENGTH - trimmed.length));

	// Groq key available? (needed for image upload)
	let hasGroqKey = $derived($healthData?.checks?.groq_key?.ok ?? false);

	// Image upload state
	let imageFile   = $state<File | null>(null);
	let imageMode   = $state<'ui' | 'document' | 'diagram'>('ui');
	let analyzing   = $state(false);
	let analyzeError = $state('');
	let analysisResult = $state<{description: string; endpoints: string[]; data_models: string[]} | null>(null);

	function fill(text: string) { description = text; }

	async function handleSubmit() {
		const t = description.trim();
		if (t.length < MIN_LENGTH) return;
		await startInterview(t);
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) handleSubmit();
	}

	function onFileChange(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		if (file.size > 4 * 1024 * 1024) {
			analyzeError = 'Image too large (max 4 MB)';
			return;
		}
		imageFile = file;
		analyzeError = '';
		analysisResult = null;
	}

	async function analyzeImage() {
		if (!imageFile) return;
		analyzing = true;
		analyzeError = '';

		try {
			const reader = new FileReader();
			const base64 = await new Promise<string>((resolve, reject) => {
				reader.onload = () => {
					const result = reader.result as string;
					// Strip data:image/...;base64, prefix
					resolve(result.split(',')[1] ?? result);
				};
				reader.onerror = reject;
				reader.readAsDataURL(imageFile!);
			});

			const res = await fetch('/api/analyze-image', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ image_base64: base64, mode: imageMode })
			});

			const data = await res.json();
			if (!res.ok) {
				analyzeError = data.error || 'Image analysis failed';
				return;
			}

			analysisResult = data;
			// Pre-fill description with the vision output
			if (data.description) {
				const extras = [
					data.endpoints?.length ? `Key endpoints: ${data.endpoints.slice(0, 3).join(', ')}` : '',
					data.data_models?.length ? `Data models: ${data.data_models.slice(0, 3).join(', ')}` : '',
				].filter(Boolean).join('. ');
				description = data.description + (extras ? '. ' + extras : '');
			}
		} catch {
			analyzeError = 'Network error — is the server running?';
		} finally {
			analyzing = false;
		}
	}
</script>

<div class="flex h-full items-center justify-center p-6 overflow-y-auto">
	<div class="w-full max-w-2xl space-y-5 py-4">
		<!-- Heading -->
		<div class="text-center">
			<h1 class="text-4xl font-bold text-[var(--text-primary)]">What do you want to build?</h1>
			<p class="mt-2 text-[var(--text-muted)]">
				Describe your idea in plain English. Pragma asks a few questions, then builds a complete backend.
			</p>
		</div>

		<!-- Expectation note -->
		<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-muted)] flex items-start gap-2">
			<span class="mt-0.5 shrink-0">&#x2139;&#xFE0F;</span>
			<span><strong class="text-[var(--text-primary)]">You'll get:</strong> a backend API + database + Docker. Use the Next.js option to also generate a React frontend. Not a mobile app builder.</span>
		</div>

		<!-- Main text input -->
		<div class="space-y-3">
			<textarea
				bind:value={description}
				onkeydown={handleKeydown}
				placeholder="Describe what you want to build…"
				rows={4}
				aria-label="Project description"
				class="w-full resize-none rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] px-5 py-4 text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none focus:ring-1 focus:ring-[var(--brand)] text-base leading-relaxed"
			></textarea>

			<button
				onclick={handleSubmit}
				disabled={trimmed.length < MIN_LENGTH}
				aria-label="Start building project"
				class="w-full rounded-xl bg-[var(--brand)] px-6 py-3.5 text-base font-semibold text-white transition-fluid hover:brightness-110 disabled:opacity-40 disabled:cursor-not-allowed"
			>
				Start Building &rarr;
			</button>
			{#if charsLeft > 0}
				<p class="text-center text-xs text-[var(--text-dim)]">{charsLeft} more character{charsLeft === 1 ? '' : 's'} needed</p>
			{:else}
				<p class="text-center text-xs text-[var(--text-dim)]">Ctrl+Enter (or &#8984;+Enter on Mac) to submit</p>
			{/if}
		</div>

		<!-- Image upload (Groq Scout vision — optional) -->
		<div class="space-y-2">
			<button
				onclick={() => (showImageUpload = !showImageUpload)}
				class="flex items-center gap-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]"
			>
				<svg class="h-4 w-4 transition-transform {showImageUpload ? 'rotate-90' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
				</svg>
				Have a mockup or diagram? Upload it
			</button>

			{#if showImageUpload}
				<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-raised)] p-4 space-y-3">
					{#if !hasGroqKey}
						<div class="rounded-lg bg-[var(--accent)]/10 border border-[var(--accent)]/30 px-4 py-3 text-sm text-[var(--text-muted)]">
							<p class="font-medium text-[var(--accent)] mb-1">Groq key required for image analysis</p>
							<p>Add a free Groq key in Settings to enable this feature. Images are analyzed using Groq's Llama 4 Scout model — they are <strong>never</strong> sent to DeepSeek.</p>
						</div>
					{:else}
						<p class="text-xs text-[var(--text-dim)]">
							Upload a UI mockup, requirements doc, or architecture diagram. Pragma will extract a project description from it automatically (uses Groq Scout — free).
						</p>

						<!-- Mode selector -->
						<div class="flex gap-2 text-xs">
							{#each [
								{ id: 'ui',       label: '&#128444;&#65039; UI mockup' },
								{ id: 'document', label: '&#128196; Document' },
								{ id: 'diagram',  label: '&#128202; Diagram' },
							] as m}
								<button
									onclick={() => { imageMode = m.id as typeof imageMode; }}
									class="rounded-md border px-2 py-1 transition-fluid
										{imageMode === m.id
											? 'border-[var(--brand)] bg-[var(--brand)]/10 text-[var(--brand-light)] font-semibold'
											: 'border-[var(--border)] text-[var(--text-muted)] hover:border-[var(--brand)]/40'}"
								>
									{@html m.label}
								</button>
							{/each}
						</div>

						<!-- File input -->
						<label class="block cursor-pointer rounded-lg border-2 border-dashed border-[var(--border)] p-6 text-center transition-fluid hover:border-[var(--brand)]/50">
							<input
								type="file"
								accept="image/jpeg,image/png,image/webp"
								onchange={onFileChange}
								class="sr-only"
							/>
							{#if imageFile}
								<p class="text-sm font-medium text-[var(--text-primary)]">&#128247; {imageFile.name}</p>
								<p class="text-xs text-[var(--text-dim)] mt-1">{(imageFile.size / 1024).toFixed(0)} KB &mdash; click to change</p>
							{:else}
								<p class="text-sm text-[var(--text-muted)]">Click to select image (JPG, PNG, WebP &mdash; max 4 MB)</p>
							{/if}
						</label>

						{#if imageFile}
							<button
								onclick={analyzeImage}
								disabled={analyzing}
								class="w-full rounded-lg bg-[var(--bg-base)] border border-[var(--border)] py-2 text-sm font-medium text-[var(--text-primary)] transition-fluid hover:bg-[var(--bg-hover)] disabled:opacity-50"
							>
								{analyzing ? '&#9203; Analyzing with Groq Scout…' : '&#128269; Analyze image & pre-fill description'}
							</button>
						{/if}

						{#if analyzeError}
							<p class="text-xs text-red-400">{analyzeError}</p>
						{/if}

						{#if analysisResult}
							<div class="rounded-lg bg-[var(--bg-base)] p-3 text-xs text-[var(--text-muted)] space-y-1">
								<p class="font-medium text-[var(--text-primary)] mb-1">&#x2705; Analysis complete &mdash; description pre-filled</p>
								{#if analysisResult.endpoints?.length}
									<p><span class="font-medium">Endpoints detected:</span> {analysisResult.endpoints.slice(0,4).join(', ')}</p>
								{/if}
								{#if analysisResult.data_models?.length}
									<p><span class="font-medium">Models detected:</span> {analysisResult.data_models.slice(0,4).join(', ')}</p>
								{/if}
								<p class="text-[var(--text-dim)]">Edit the description above if needed, then click Start Building.</p>
							</div>
						{/if}
					{/if}
				</div>
			{/if}
		</div>

		<!-- Example cards -->
		<div class="space-y-3">
			<button
				onclick={() => (showExamples = !showExamples)}
				class="flex items-center gap-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]"
			>
				<svg class="h-4 w-4 transition-transform {showExamples ? 'rotate-90' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
				</svg>
				Need inspiration?
			</button>

			{#if showExamples}
				<div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
					{#each [
						{ title: 'Booking page',     text: 'Booking page for my barbershop with appointment slots, staff selection, and email confirmations',  sub: 'Appointments, staff, email' },
						{ title: 'Expense tracker',  text: 'Expense tracker with charts, categories, and monthly budget limits',                                sub: 'Charts, categories, budgets' },
						{ title: 'Q&A forum',        text: 'Q&A forum where users can post questions and vote on the best answers',                             sub: 'Posts, voting, best answers' },
						{ title: 'Job board',        text: 'Job board for remote positions with company profiles, filtering, and application tracking',         sub: 'Remote jobs, filtering' },
						{ title: 'Leave management', text: 'A leave management app for my 20-person company with approval workflows and calendar integration',  sub: 'Approvals, calendar, team' },
					] as ex}
						<button
							onclick={() => fill(ex.text)}
							class="rounded-lg border border-[var(--border)] bg-[var(--bg-raised)] px-4 py-3 text-left text-sm text-[var(--text-muted)] transition-fluid hover:border-[var(--brand)] hover:text-[var(--text-primary)]"
						>
							<span class="block font-medium text-[var(--text-primary)]">{ex.title}</span>
							<span class="text-xs">{ex.sub}</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>
	</div>
</div>
