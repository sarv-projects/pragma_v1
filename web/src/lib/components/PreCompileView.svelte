<script lang="ts">
	import { manifest, showPreCompile, selectedProfile, sendUpdateManifest } from '$lib/stores/ws';

	interface ProfileInfo {
		id: string;
		name: string;
		description: string;
		beginner_label?: string;
		language: string;
	}

	let projectName = $state('');
	let additions = $state('');
	let profileInfo = $state<ProfileInfo | null>(null);

	// Initialize project name from manifest
	$effect(() => {
		const m = $manifest as any;
		if (m) {
			projectName = m.project_name || (m.description ? m.description.slice(0, 40) : '');
		}
	});

	// Look up the auto-selected profile details from the server
	$effect(() => {
		const pid = $selectedProfile;
		if (!pid) return;
		fetch('/api/profiles')
			.then((r) => r.json())
			.then((data: ProfileInfo[]) => {
				const found = data.find((p) => p.id === pid);
				if (found) {
					profileInfo = found;
				} else {
					// Profile ID exists but metadata not returned — display ID as fallback
					profileInfo = { id: pid, name: pid, description: '', language: '' };
				}
			})
			.catch(() => {
				profileInfo = { id: pid, name: pid, description: '', language: '' };
			});
	});

	function handleProceed() {
		sendUpdateManifest(projectName.trim() || 'My Project', additions.trim());

		manifest.update((m: any) => {
			if (!m) m = {};
			m.project_name = projectName.trim() || m.project_name || 'My Project';
			if (additions.trim()) {
				m.description = (m.description || '') + '\n\nAdditional notes: ' + additions.trim();
			}
			return m;
		});
		showPreCompile.set(false);
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
			handleProceed();
		}
	}
</script>

<div class="flex h-full items-center justify-center px-4">
	<div class="w-full max-w-lg rounded-2xl border border-[var(--border)] bg-[var(--bg-raised)] p-8 shadow-xl enter-from-below">
		<div class="mb-6 text-center">
			<div class="mb-3 text-4xl">&#9997;&#65039;</div>
			<h2 class="text-2xl font-bold text-[var(--text-primary)]">Name your project</h2>
			<p class="mt-2 text-sm text-[var(--text-muted)]">
				Review the auto-selected tech stack and give your project a name.
			</p>
		</div>

		<div class="space-y-5">
			<!-- Read-only profile display -->
			<div class="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4">
				<p class="mb-1 text-xs font-medium uppercase tracking-wider text-[var(--text-dim)]">Tech stack</p>
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm font-semibold text-[var(--text-primary)]">
							{profileInfo?.name || $selectedProfile || 'Auto-selected'}
						</p>
						{#if profileInfo?.description}
							<p class="text-xs text-[var(--text-muted)]">{profileInfo.description}</p>
						{/if}
					</div>
					<span class="rounded-md bg-[var(--brand)]/10 px-2 py-0.5 text-xs font-medium text-[var(--brand-light)]">
						{profileInfo?.language || ''}
					</span>
				</div>
			</div>

			<!-- Project name -->
			<div>
				<label for="project-name" class="mb-1.5 block text-sm font-medium text-[var(--text-primary)]">
					Project name
				</label>
				<input
					id="project-name"
					type="text"
					bind:value={projectName}
					onkeydown={handleKeydown}
					placeholder="My Awesome App"
					class="w-full rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--brand)] focus:outline-none focus:ring-1 focus:ring-[var(--brand)]"
				/>
			</div>

			<!-- Anything to add -->
			<div>
				<label for="additions" class="mb-1.5 block text-sm font-medium text-[var(--text-primary)]">
					Anything else to add before building?
				</label>
				<textarea
					id="additions"
					bind:value={additions}
					onkeydown={handleKeydown}
					placeholder="Optional: extra requirements, constraints, or preferences…"
					rows={3}
					class="w-full resize-none rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-dim)] focus:border-[var(--border)] focus:outline-none focus:ring-1 focus:ring-[var(--border)]"
				></textarea>
			</div>

			<!-- Proceed button -->
			<button
				onclick={handleProceed}
				class="w-full rounded-xl bg-[var(--brand)] py-3 text-sm font-semibold text-white transition-fluid hover:scale-[1.01] hover:brightness-110 active:scale-[0.99]"
			>
				Proceed &rarr;
			</button>
			<p class="text-center text-xs text-[var(--text-dim)]">Ctrl+Enter (or ⌘+Enter on Mac) to proceed</p>
		</div>
	</div>
</div>
