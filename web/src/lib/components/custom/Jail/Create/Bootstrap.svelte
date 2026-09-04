<script lang="ts">
	import { getBootstraps, createBootstrap, deleteBootstrap } from '$lib/api/jail/bootstrap';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { fade } from 'svelte/transition';

	interface Props {
		open: boolean;
		pool: string;
		hostname?: string;
		onComplete: () => void;
	}

	let { open = $bindable(), pool, hostname, onComplete }: Props = $props();

	let entries = $state<BootstrapEntry[]>([]);
	let loading = $state(false);
	let loadError = $state('');
	let starting = $state<Record<string, boolean>>({});
	let deleting = $state<Record<string, boolean>>({});
	let pollInterval: ReturnType<typeof setInterval> | null = null;
	let fetchController: AbortController | null = null;

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

	function responseError(response: APIResponse, fallback: string): string {
		if (Array.isArray(response.error)) return response.error.join(', ');
		return response.error || response.message || fallback;
	}

	function cancelFetch() {
		fetchController?.abort();
		fetchController = null;
		loading = false;
	}

	async function fetchEntries(minDelay = 0, reportError = false): Promise<boolean> {
		if (!pool) return false;
		cancelFetch();
		const controller = new AbortController();
		fetchController = controller;
		const requestedPool = pool;
		const requestedHostname = hostname;
		loading = true;
		try {
			const [result] = await Promise.all([
				getBootstraps(requestedPool, {
					hostname: requestedHostname,
					signal: controller.signal
				}),
				minDelay > 0 ? new Promise((r) => setTimeout(r, minDelay)) : Promise.resolve()
			]);
			if (controller.signal.aborted || requestedPool !== pool || requestedHostname !== hostname) {
				return false;
			}
			if (isAPIResponse(result)) {
				loadError = responseError(result, 'Failed to load bootstraps');
				if (reportError) handleAPIError(result);
				return false;
			}

			entries = result;
			loadError = '';
			return true;
		} catch (error) {
			if (isRequestCancellation(error)) return false;
			loadError = error instanceof Error ? error.message : 'Failed to load bootstraps';
			return false;
		} finally {
			if (fetchController === controller) {
				fetchController = null;
				loading = false;
			}
		}
	}

	function startPolling() {
		stopPolling();
		pollInterval = setInterval(async () => {
			const refreshed = await fetchEntries();
			if (!refreshed) return;
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

	watch([() => open, () => pool, () => hostname], ([isOpen]) => {
		stopPolling();
		if (!isOpen) {
			fetchController?.abort();
			fetchController = null;
			return;
		}
		cancelFetch();
		entries = [];
		loadError = '';
		void fetchEntries(600, true).then((refreshed) => {
			if (refreshed && anyActive(entries)) startPolling();
		});
	});

	onDestroy(() => {
		stopPolling();
		cancelFetch();
	});

	async function handleDelete(entry: BootstrapEntry) {
		const key = entryKey(entry);
		deleting[key] = true;
		try {
			const response = await deleteBootstrap(entry.pool, entry.name, { hostname });
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error(`Failed to delete bootstrap: ${responseError(response, 'Unknown error')}`, {
					position: 'bottom-center'
				});
				return;
			}

			if (!(await fetchEntries(0, true))) return;
			onComplete();
		} catch (e: unknown) {
			if (isRequestCancellation(e)) return;
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
			const response = await createBootstrap(
				{
					pool: entry.pool,
					major: entry.major,
					minor: entry.minor,
					type: entry.type
				},
				{ hostname }
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error(`Bootstrap failed to start: ${responseError(response, 'Unknown error')}`, {
					position: 'bottom-center'
				});
				return;
			}

			if (!(await fetchEntries(0, true))) return;
			if (anyActive(entries)) {
				startPolling();
			} else {
				onComplete();
			}
		} catch (e: unknown) {
			if (isRequestCancellation(e)) return;
			const msg = e instanceof Error ? e.message : String(e);
			toast.error(`Bootstrap failed to start: ${msg}`, { position: 'bottom-center' });
		} finally {
			starting[key] = false;
		}
	}
</script>

<Dialog.Root
	bind:open
	onOpenChangeComplete={(isOpen) => {
		if (isOpen) return;
		entries = [];
		loadError = '';
		loading = false;
	}}
>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex w-[90%] max-w-xl -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0 p-5 transition-all duration-300 ease-in-out"
		showCloseButton={true}
		onClose={() => {
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--download-box-outline] h-5 w-5"></span>
					<span>Bootstrap Jail Bases</span>
				</div>
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
					{#each [1, 2] as i (i)}
						<div class="animate-pulse rounded-md border p-3">
							<div class="flex items-center justify-between gap-2">
								<div class="h-4 w-36 rounded bg-muted"></div>
								<div class="h-6 w-16 rounded bg-muted"></div>
							</div>
							<div class="mt-2 h-1.5 w-full rounded bg-muted"></div>
						</div>
					{/each}
				{:else if loadError && entries.length === 0}
					<div class="flex flex-col items-center gap-2 py-6 text-center text-sm text-destructive">
						<span>{loadError}</span>
						<Button size="sm" variant="outline" onclick={() => void fetchEntries(0, true)}>
							Retry
						</Button>
					</div>
				{:else if entries.length === 0}
					<div class="py-6 text-center text-sm text-muted-foreground">
						No supported versions available.
					</div>
				{:else}
					<div class="flex flex-col gap-3" transition:fade={{ duration: 400, delay: 100 }}>
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
										{:else if entry.status === 'orphaned'}
											<span class="text-xs text-amber-600 dark:text-amber-400">
												Dataset exists without a bootstrap record. Delete it before recreating.
											</span>
										{/if}
									</div>

									<div class="shrink-0">
										{#if entry.status === 'completed'}
											<div class="flex items-center gap-1.5">
												<span
													class="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800 dark:bg-green-900/30 dark:text-green-400"
												>
													<SpanWithIcon
														icon="icon-[mdi--check-circle-outline]"
														size="h-3 w-3"
														gap="gap-1"
														title="Installed"
													/>
												</span>
												<Button
													size="sm"
													variant="outline"
													class="h-6 w-6 p-0"
													disabled={deleting[key] || starting[key]}
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
											<div class="flex items-center gap-1.5">
												{#if !entry.exists}
													<Button
														size="sm"
														variant="outline"
														class="h-7 text-xs"
														disabled={starting[key] || deleting[key]}
														onclick={() => handleBootstrap(entry)}
													>
														{#if starting[key]}
															<span class="icon-[mdi--loading] h-3 w-3 animate-spin"></span>
														{:else}
															Retry
														{/if}
													</Button>
												{/if}
												<Button
													size="sm"
													variant="outline"
													class="h-7 w-7 p-0"
													disabled={deleting[key] || starting[key]}
													onclick={() => handleDelete(entry)}
													title="Clear failed bootstrap"
												>
													{#if deleting[key]}
														<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
													{:else}
														<span class="icon-[mdi--trash-can-outline] h-4 w-4 text-destructive"
														></span>
													{/if}
												</Button>
											</div>
										{:else if entry.status === 'orphaned'}
											<div class="flex items-center gap-1.5">
												<span
													class="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-400"
												>
													Orphaned
												</span>
												<Button
													size="sm"
													variant="outline"
													class="h-7 w-7 p-0"
													disabled={deleting[key]}
													onclick={() => handleDelete(entry)}
													title="Delete orphaned bootstrap dataset"
												>
													{#if deleting[key]}
														<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
													{:else}
														<span class="icon-[mdi--trash-can-outline] h-4 w-4 text-destructive"
														></span>
													{/if}
												</Button>
											</div>
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
								{:else if entry.status === 'orphaned'}
									<div class="mt-2">
										<Progress value={100} max={100} class="h-1.5" progressClass="bg-amber-500" />
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
