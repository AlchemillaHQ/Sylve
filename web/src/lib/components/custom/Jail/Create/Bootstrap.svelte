<script lang="ts">
	import { getBootstraps, createBootstrap, deleteBootstrap } from '$lib/api/jail/bootstrap';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		pool: string;
		onComplete: () => void;
	}

	let { open = $bindable(), pool, onComplete }: Props = $props();

	let entries = $state<BootstrapEntry[]>([]);
	let loading = $state(false);
	let starting = $state<Record<string, boolean>>({});
	let deleting = $state<Record<string, boolean>>({});
	let pollInterval: ReturnType<typeof setInterval> | null = null;

	const phaseMap: Record<string, { label: string; pct: number }> = {
		'': { label: 'Queued...', pct: 0 },
		creating_dataset: { label: 'Creating ZFS dataset...', pct: 5 },
		copying_keys: { label: 'Copying signing keys...', pct: 15 },
		writing_repo_conf: { label: 'Writing repository config...', pct: 20 },
		updating_repo: { label: 'Fetching package index...', pct: 35 },
		installing: { label: 'Installing packages...', pct: 80 },
		writing_config: { label: 'Writing jail config...', pct: 95 },
		pre_check: { label: 'Pre-flight checks...', pct: 2 }
	};

	function getPhaseInfo(phase: string): { label: string; pct: number } {
		return phaseMap[phase] ?? { label: phase, pct: 50 };
	}

	function isActive(entry: BootstrapEntry) {
		return entry.status === 'running' || entry.status === 'pending';
	}

	function anyActive(list: BootstrapEntry[]) {
		return list.some(isActive);
	}

	function entryKey(e: BootstrapEntry) {
		return `${e.pool}:${e.name}`;
	}

	async function fetchEntries() {
		if (!pool) return;
		loading = true;
		try {
			entries = await getBootstraps(pool);
		} catch {
			// silently ignore polling errors
		} finally {
			loading = false;
		}
	}

	function startPolling() {
		stopPolling();
		pollInterval = setInterval(async () => {
			await fetchEntries();
			if (!anyActive(entries)) {
				stopPolling();
				onComplete();
			}
		}, 3000);
	}

	function stopPolling() {
		if (pollInterval !== null) {
			clearInterval(pollInterval);
			pollInterval = null;
		}
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				void fetchEntries().then(() => {
					if (anyActive(entries)) startPolling();
				});
			} else {
				stopPolling();
			}
		}
	);

	async function handleDelete(entry: BootstrapEntry) {
		const key = entryKey(entry);
		deleting[key] = true;
		try {
			await deleteBootstrap(entry.pool, entry.name);
			await fetchEntries();
			onComplete();
		} catch (e: unknown) {
			const msg = e instanceof Error ? e.message : String(e);
			toast.error(`Failed to delete bootstrap: ${msg}`, { position: 'bottom-center' });
		} finally {
			deleting[key] = false;
		}
	}

	async function handleBootstrap(entry: BootstrapEntry) {
		const key = entryKey(entry);
		starting[key] = true;
		try {
			await createBootstrap({
				pool: entry.pool,
				major: entry.major,
				minor: entry.minor,
				type: entry.type
			});
			await fetchEntries();
			startPolling();
		} catch (e: unknown) {
			const msg = e instanceof Error ? e.message : String(e);
			toast.error(`Bootstrap failed to start: ${msg}`, { position: 'bottom-center' });
		} finally {
			starting[key] = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex w-[90%] max-w-xl -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0 p-5 transition-all duration-300 ease-in-out"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--download-box-outline] h-5 w-5"></span>
					<span>Bootstrap Jail Bases</span>
				</div>
				<Button size="sm" variant="link" class="h-4" onclick={() => (open = false)} title="Close">
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</Dialog.Title>
			<Dialog.Description>
				Bootstrap a directory with pkgbase to create a jail base. This sets up the required files in
				a ZFS dataset, which can then be used to create new jails.
			</Dialog.Description>
		</Dialog.Header>

		<div class="mt-4 flex flex-col gap-4">
			<!-- Bootstrap entries list -->
			<div class="flex flex-col gap-3">
				{#if loading && entries.length === 0}
					<div class="flex items-center justify-center py-6 text-sm text-muted-foreground">
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Loading...
					</div>
				{:else if entries.length === 0}
					<div class="py-6 text-center text-sm text-muted-foreground">
						No supported versions available.
					</div>
				{:else}
					{#each entries as entry (entryKey(entry))}
						{@const phaseInfo = getPhaseInfo(entry.phase)}
						{@const key = entryKey(entry)}
						{@const longStep = entry.phase === 'updating_repo' || entry.phase === 'installing'}
						<div class="rounded-md border p-3">
							<div class="flex items-center justify-between gap-2">
								<div class="flex flex-col gap-0.5">
									<span class="text-sm font-medium">{entry.label}</span>
									{#if entry.status === 'running' || entry.status === 'pending'}
										<span class="flex items-center gap-1 text-xs text-muted-foreground">
											<span class="icon-[mdi--loading] h-3 w-3 animate-spin"></span>
											{entry.status === 'pending' ? 'Queued...' : phaseInfo.label}
										</span>
									{:else if entry.status === 'failed'}
										<span class="text-xs text-destructive">{entry.error || 'Unknown error'}</span>
									{/if}
								</div>

								<div class="shrink-0">
									{#if entry.status === 'completed'}
										<div class="flex items-center gap-1.5">
											<span
												class="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800 dark:bg-green-900/30 dark:text-green-400"
											>
												<span class="icon-[mdi--check-circle-outline] h-3 w-3"></span>
												Installed
											</span>
											<Button
												size="sm"
												variant="outline"
												disabled={deleting[key]}
												onclick={() => handleDelete(entry)}
												title="Delete bootstrap"
											>
												{#if deleting[key]}
													<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
												{:else}
													<span
														class="icon-[mdi--trash-can-outline] h-4 w-4 text-destructive hover:text-destructive"
													></span>
												{/if}
											</Button>
										</div>
									{:else if entry.status === 'running' || entry.status === 'pending'}
										<span
											class="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-800 dark:bg-blue-900/30 dark:text-blue-400"
										>
											<span class="icon-[mdi--loading] h-3 w-3 animate-spin"></span>
											{entry.status === 'pending' ? 'Pending' : 'Running'}
										</span>
									{:else if entry.status === 'failed'}
										<Button
											size="sm"
											variant="outline"
											class="h-7 text-xs"
											disabled={starting[key]}
											onclick={() => handleBootstrap(entry)}
										>
											{#if starting[key]}
												<span class="icon-[mdi--loading] h-3 w-3 animate-spin"></span>
											{:else}
												Retry
											{/if}
										</Button>
									{:else}
										<Button
											size="sm"
											variant="outline"
											class="h-7 text-xs"
											disabled={starting[key]}
											onclick={() => handleBootstrap(entry)}
										>
											{#if starting[key]}
												<span class="icon-[mdi--loading] h-3 w-3 animate-spin"></span>
											{:else}
												Bootstrap
											{/if}
										</Button>
									{/if}
								</div>
							</div>

							{#if entry.status === 'running' || entry.status === 'pending'}
								<div class="mt-2">
									<Progress
										value={entry.status === 'pending' ? 0 : phaseInfo.pct}
										max={100}
										class="h-1.5"
										progressClass={longStep
											? 'bg-blue-600 animate-pulse'
											: 'bg-blue-600 transition-all duration-700'}
									/>
									<div class="mt-1 text-right text-xs text-muted-foreground">
										{entry.status === 'pending' ? 0 : phaseInfo.pct}%
									</div>
								</div>
							{:else if entry.status === 'failed'}
								<div class="mt-2">
									<Progress value={0} max={100} class="h-1.5" progressClass="bg-destructive" />
								</div>
							{:else if entry.status === 'completed'}
								<div class="mt-2">
									<Progress value={100} max={100} class="h-1.5" progressClass="bg-green-600" />
								</div>
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
