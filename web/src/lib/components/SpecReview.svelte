<script lang="ts">
	import { specData, specFileCount, specTestCount, approveSpec, rejectSpec, refineSpec } from '$lib/stores/ws';

	interface SpecFile { path: string; role?: string; exports?: string[]; public_api?: any[]; description?: string }

	let files = $derived<SpecFile[]>(($specData as any)?.files || []);
	let showTechnicalSpec = $state(false);
	
	let refineInput = $state('');
	let isRefining = $state(false);

	// When specData changes (from server), reset refining state
	$effect(() => {
		if ($specData) {
			isRefining = false;
			refineInput = '';
		}
	});

	function handleRefine(e: KeyboardEvent) {
		if (e.key === 'Enter' && refineInput.trim() && !isRefining) {
			isRefining = true;
			refineSpec(refineInput.trim());
		}
	}

	// Extract features grouped by role
	let features = $derived(() => {
		const featureFiles = files.filter(f => f.role && f.role !== 'model' && f.role !== 'config' && f.role !== 'test');
		const grouped = new Map<string, SpecFile[]>();
		for (const f of featureFiles) {
			const role = f.role || 'other';
			if (!grouped.has(role)) grouped.set(role, []);
			grouped.get(role)!.push(f);
		}
		return grouped;
	});

	// Extract data models
	let models = $derived(() => {
		return files.filter(f => f.role === 'model');
	});

	// Extract screens/pages (route files or files with 'page'/'screen'/'view' in the path)
	let screens = $derived(() => {
		return files.filter(f =>
			f.path.includes('route') || f.path.includes('page') ||
			f.path.includes('screen') || f.path.includes('view') ||
			f.role === 'page' || f.role === 'view'
		);
	});

	// Extract integrations (files with 'middleware', 'integration', 'client', 'service' in path or role)
	let integrations = $derived(() => {
		return files.filter(f =>
			f.role === 'middleware' || f.role === 'integration' || f.role === 'service' ||
			f.path.includes('middleware') || f.path.includes('integration')
		);
	});

	// File tree grouping for technical view
	let grouped = $derived(() => {
		const map = new Map<string, SpecFile[]>();
		for (const f of files) {
			const idx = f.path.lastIndexOf('/');
			const dir = idx >= 0 ? f.path.slice(0, idx) : '.';
			if (!map.has(dir)) map.set(dir, []);
			map.get(dir)!.push(f);
		}
		return map;
	});

	function formatRole(role: string): string {
		return role.replace(/[_-]/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
	}

	function getFileName(path: string): string {
		const parts = path.split('/');
		return parts[parts.length - 1] || path;
	}
</script>

<div class="flex h-full flex-col px-4 py-6 md:px-8">
	<div class="mx-auto w-full max-w-3xl">
		<!-- Header -->
		<h2 class="mb-1 text-2xl font-bold text-[var(--text-primary)]">Spec Review</h2>
		<p class="mb-4 text-sm text-[var(--text-muted)]">
			Pragma designed your project. Here is what will be built.
		</p>

		<!-- Stats -->
		<div class="mb-6 flex gap-4 text-sm">
			<span class="rounded-lg bg-[var(--brand)]/10 px-3 py-1 text-[var(--brand-light)]">
				{$specFileCount} files
			</span>
			<span class="rounded-lg bg-[var(--accent)]/10 px-3 py-1 text-[var(--accent)]">
				{$specTestCount} tests
			</span>
		</div>

		<!-- Feature summary sections -->
		<div class="mb-6 space-y-4 max-h-[50vh] overflow-y-auto">
			<!-- Features by role -->
			{#if features().size > 0}
				<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4">
					<h3 class="mb-2 text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
						<span>&#9889;</span> Features
					</h3>
					{#each [...features()] as [role, roleFiles]}
						<div class="mb-2 ml-2">
							<p class="text-xs font-medium uppercase tracking-wider text-[var(--accent)] mb-1">{formatRole(role)}</p>
							<ul class="ml-4 space-y-0.5">
								{#each roleFiles as f}
									<li class="text-sm text-[var(--text-muted)]">{f.description || getFileName(f.path)}</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			{/if}

			<!-- Data Models -->
			{#if models().length > 0}
				<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4">
					<h3 class="mb-2 text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
						<span>&#128451;</span> Data Models
					</h3>
					<ul class="ml-4 space-y-0.5">
						{#each models() as f}
							<li class="text-sm text-[var(--text-muted)]">{f.description || getFileName(f.path)}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<!-- Screens / Pages -->
			{#if screens().length > 0}
				<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4">
					<h3 class="mb-2 text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
						<span>&#128187;</span> Screens &amp; Pages
					</h3>
					<ul class="ml-4 space-y-0.5">
						{#each screens() as f}
							<li class="text-sm text-[var(--text-muted)]">{f.description || getFileName(f.path)}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<!-- Integrations -->
			{#if integrations().length > 0}
				<div class="rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4">
					<h3 class="mb-2 text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
						<span>&#128279;</span> Integrations
					</h3>
					<ul class="ml-4 space-y-0.5">
						{#each integrations() as f}
							<li class="text-sm text-[var(--text-muted)]">{f.description || getFileName(f.path)}</li>
						{/each}
					</ul>
				</div>
			{/if}
		</div>

		<!-- Collapsible technical spec -->
		<div class="mb-6">
			<button
				onclick={() => (showTechnicalSpec = !showTechnicalSpec)}
				aria-expanded={showTechnicalSpec}
				class="flex items-center gap-1.5 text-sm text-[var(--text-muted)] transition-fluid hover:text-[var(--brand-light)]"
			>
				<svg class="h-4 w-4 transition-transform {showTechnicalSpec ? 'rotate-90' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
				</svg>
				View technical spec
			</button>

			{#if showTechnicalSpec}
				<div class="mt-3 max-h-[40vh] overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--bg-base)] p-4">
					{#each [...grouped()] as [dir, dirFiles]}
						<div class="mb-3">
							<p class="mb-1 text-xs font-medium uppercase tracking-wider text-[var(--accent)]">&#128193; {dir}</p>
							{#each dirFiles as f}
								<div class="ml-4 flex items-center gap-2 py-0.5 text-sm text-[var(--text-primary)]">
									<span class="text-[var(--text-dim)]">&#183;</span>
									<span>{f.path.split('/').pop()}</span>
									{#if f.role}
										<span class="text-xs text-[var(--text-dim)]">[{f.role}]</span>
									{/if}
								</div>
							{/each}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<!-- Refine Input -->
		<div class="mb-6">
			<div class="relative">
				<input
					type="text"
					placeholder={isRefining ? "Refining plan..." : "Tweak this plan (e.g. 'add a stripe customer id to user')..."}
					bind:value={refineInput}
					onkeydown={handleRefine}
					disabled={isRefining}
					class="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none focus:ring-1 focus:ring-[var(--brand)] disabled:opacity-50"
				/>
				{#if isRefining}
					<div class="absolute right-3 top-3 h-4 w-4 animate-spin rounded-full border-2 border-[var(--brand)] border-t-transparent"></div>
				{/if}
			</div>
		</div>

		<!-- Simple approval panel -->
		<div class="rounded-xl border border-[var(--brand)]/25 bg-[var(--brand)]/5 p-4 mb-2">
			<div class="flex items-center justify-between mb-3">
				<p class="text-sm font-medium text-[var(--text-primary)]">
					{$specFileCount} files &middot; {$specTestCount} tests &middot; looks good?
				</p>
				<span class="text-xs text-[var(--text-dim)]">~$0.01&ndash;$0.03</span>
			</div>
			<div class="flex gap-3">
				<button
					onclick={approveSpec}
					disabled={isRefining}
					class="flex-1 rounded-xl bg-[var(--brand)] py-3 text-sm font-semibold text-white transition-fluid hover:scale-[1.01] hover:brightness-110 active:scale-[0.99] disabled:opacity-50 disabled:pointer-events-none"
				>
					&#10003; Continue &rarr;
				</button>
				<button
					onclick={rejectSpec}
					disabled={isRefining}
					class="rounded-xl border border-[var(--border)] px-6 py-3 text-sm font-medium text-[var(--text-muted)] transition-fluid hover:bg-[var(--bg-hover)] active:scale-[0.98] disabled:opacity-50 disabled:pointer-events-none"
				>
					Start over
				</button>
			</div>
		</div>
		<p class="text-center text-xs text-[var(--text-dim)]">
			Rejecting regenerates the plan (~$0.01&ndash;$0.03). Review the technical spec above before deciding.
		</p>
	</div>
</div>
