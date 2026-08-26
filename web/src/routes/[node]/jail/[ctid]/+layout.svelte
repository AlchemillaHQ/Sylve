<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { resolve } from '$app/paths';
	import { setContext, untrack } from 'svelte';
	import { page } from '$app/state';
	import { deleteJail, getJailState, getSimpleJailByCTID, jailAction } from '$lib/api/jail/jail';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import { jailPowerSignal, reload } from '$lib/stores/api.svelte';
	import type { JailState, SimpleJail } from '$lib/types/jail/jail';
	import { getJailLifecycleBadgeStyle, removeStaleJailCacheByCTID } from '$lib/utils/jail/jail';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { parseGuestDeletionData, type GFSStep } from '$lib/types/common';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import MigrateModal from '$lib/components/custom/VM/MigrateModal.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let ctId = $derived.by(() => {
		const value = Number(page.url.pathname.split('/')[3]);
		return Number.isFinite(value) ? value : 0;
	});
	let node = $derived(String(page.params.node || ''));

	let jailChildRoute = $derived.by(() => {
		const segments = page.url.pathname.split('/').filter(Boolean);
		const jailIndex = segments.indexOf('jail');
		if (jailIndex === -1) return '';
		return segments[jailIndex + 2] ?? '';
	});
	let isConsolePage = $derived.by(() => jailChildRoute === 'console');
	let isSummaryPage = $derived.by(() => jailChildRoute === '' || jailChildRoute === 'summary');

	type SimpleJailSnapshot = { identity: string; value: SimpleJail | null };
	type JailStateSnapshot = { identity: string; value: JailState | null };
	type JailLayoutData = {
		node?: string;
		ctId?: number;
		jail?: SimpleJail | null;
		state?: JailState | null;
	};

	const initialLayoutData = untrack(() => page.data as JailLayoutData);
	const initialLayoutIdentity = `${initialLayoutData.node || ''}\u0000${initialLayoutData.ctId || 0}`;
	let jailIdentity = $derived(`${node}\u0000${ctId}`);

	const jailResource = resource(
		[() => node, () => ctId],
		async ([hostname, currentCtID], _, { signal }): Promise<SimpleJailSnapshot> => {
			const identity = `${hostname}\u0000${currentCtID}`;
			if (!currentCtID) return { identity, value: null };
			const result = await getSimpleJailByCTID(currentCtID, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load jail');
			}
			await updateCache(`simple-jail-${currentCtID}`, result, hostname);
			return { identity, value: result };
		},
		{
			initialValue: {
				identity: initialLayoutIdentity,
				value: initialLayoutData.jail ?? null
			}
		}
	);

	const stateResource = resource(
		[() => node, () => ctId],
		async ([hostname, currentCtID], _, { signal }): Promise<JailStateSnapshot> => {
			const identity = `${hostname}\u0000${currentCtID}`;
			if (!currentCtID) return { identity, value: null };
			const result = await getJailState(currentCtID, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load jail state');
			}
			await updateCache(`jail-${currentCtID}-state`, result, hostname);
			return { identity, value: result };
		},
		{
			initialValue: {
				identity: initialLayoutIdentity,
				value: initialLayoutData.state ?? null
			}
		}
	);

	const jail = {
		get current(): SimpleJail | null {
			return jailResource.current.identity === jailIdentity
				? jailResource.current.value
				: ((page.data as JailLayoutData).jail ?? null);
		},
		refetch: () => jailResource.refetch()
	};

	const jState = {
		get current(): JailState | null {
			return stateResource.current.identity === jailIdentity
				? stateResource.current.value
				: ((page.data as JailLayoutData).state ?? null);
		},
		refetch: () => stateResource.refetch()
	};

	setContext('jailState', jState);

	let hasActiveLifecycleTask = $derived(!!jState.current?.pendingAction);
	let isMigrationActive = $derived(jState.current?.pendingAction === 'migrate');
	let lifecycleActionBadge = $derived(
		getJailLifecycleBadgeStyle(jState.current?.pendingAction || '')
	);
	let shouldHideActionButtons = $derived(hasActiveLifecycleTask);
	let showMigrateModal = $state(false);
	let actionRequestInFlight = $state(false);

	let sourceNodeUuid = $derived.by(() => {
		const nds = (page.data as Record<string, unknown>).nodes;
		if (!nds || !Array.isArray(nds)) return '';
		const self = (nds as ClusterNode[]).find((n) => n.guestIDs?.includes(ctId));
		if (self) return self.nodeUUID;
		const selfNode = (page.params.node || '').toLowerCase();
		const byName = (nds as ClusterNode[]).find((n) => n.hostname.toLowerCase() === selfNode);
		return byName?.nodeUUID ?? '';
	});

	let availableNodeCount = $derived.by(() => {
		const nds = (page.data as Record<string, unknown>).nodes;
		if (!nds || !Array.isArray(nds)) return 1;
		const selfUuid = sourceNodeUuid;
		const selfHostname = (page.params.node || '').toLowerCase();
		let count = 0;
		for (const n of nds as ClusterNode[]) {
			if (n.nodeUUID === '' || n.status !== 'online') continue;
			if (n.nodeUUID === selfUuid) continue;
			if (!selfUuid && n.hostname.toLowerCase() === selfHostname) continue;
			count++;
		}
		return count;
	});

	class SummaryBarExtras {
		logsLength = $state(0);
		showLogsCallback = $state<() => void>(() => {});
		gfsStep = $state<GFSStep>('hourly');
		refetchStats = $state<(step?: GFSStep) => void>(() => {});
		active = $state(false);
	}

	const summaryBarExtras = new SummaryBarExtras();
	setContext('jailSummaryBarExtras', summaryBarExtras);

	const visible = new IsDocumentVisible();
	let isDeleteInFlight = $state(false);

	let modalState = $state({
		isDeleteOpen: false,
		deleteMacs: false,
		deleteRootFS: false,
		title: '',
		loading: {
			open: false,
			title: '',
			description: '',
			iconColor: ''
		}
	});

	async function refreshJailState() {
		await Promise.all([jail.refetch(), jState.refetch()]);
	}

	watch(
		() => ctId,
		(newCtId) => {
			if (newCtId) {
				refreshJailState();
			}
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (visible.current && ctId && !isDeleteInFlight) {
				jState.refetch();
			}
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle && ctId && !isDeleteInFlight) {
				refreshJailState();
			}
		}
	);

	function openDeleteModal() {
		if (!jail.current) return;
		modalState.deleteMacs = false;
		modalState.deleteRootFS = false;
		modalState.title = `${jail.current.name} (${jail.current.ctId})`;
		modalState.isDeleteOpen = true;
	}

	async function handleDelete() {
		if (!jail.current || isDeleteInFlight) return;
		const target = {
			ctId: jail.current.ctId,
			name: jail.current.name,
			hostname: node
		};
		isDeleteInFlight = true;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = 'Deleting Jail';
		modalState.loading.description = `Please wait while Jail <b>${target.name} (${target.ctId})</b> is being deleted`;
		modalState.loading.iconColor = 'text-red-500';

		try {
			const result = await deleteJail(
				target.ctId,
				modalState.deleteMacs,
				modalState.deleteRootFS,
				target.hostname
			);

			if (result.status === 'error') {
				await Promise.allSettled([jail.refetch(), jState.refetch()]);
				toast.error(
					result.message === 'guest_delete_requires_replication_policy_removed'
						? 'Remove the replication policy before deleting this jail'
						: 'Error deleting jail',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
				return;
			}

			reload.leftPanel = true;
			const deletionData = parseGuestDeletionData(result.data);
			const cleanupWarnings = deletionData.warnings;
			const retainedDatasets = deletionData.retainedDatasets;
			await removeStaleJailCacheByCTID(target.ctId, target.hostname);
			await useSafeGoto(resolve('/[node]/summary', { node: target.hostname }));
			if (cleanupWarnings.length > 0) {
				toast.warning(
					`Jail deleted, but cleanup was incomplete${retainedDatasets.length > 0 ? `: ${retainedDatasets.join(', ')}` : ''}`,
					{ duration: 8000, position: 'bottom-center' }
				);
			} else if (retainedDatasets.length > 0) {
				toast.warning(`Jail deleted; root filesystem retained at ${retainedDatasets.join(', ')}`, {
					duration: 8000,
					position: 'bottom-center'
				});
			} else {
				toast.success('Jail deleted', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		} catch (error) {
			await Promise.allSettled([jail.refetch(), jState.refetch()]);
			toast.error(error instanceof Error ? error.message : 'Error deleting jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} finally {
			modalState.loading.open = false;
			isDeleteInFlight = false;
		}
	}

	async function handleStop() {
		if (!jail.current || actionRequestInFlight) return;
		const targetCTID = jail.current.ctId;
		actionRequestInFlight = true;

		try {
			const result = await jailAction(targetCTID, 'stop', node);
			if (isAPIResponse(result)) {
				toast.error(
					result.message === 'lifecycle_task_in_progress' ||
						result.message === 'migration_in_progress'
						? 'Jail action already in progress'
						: 'Error stopping jail',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
				return;
			}

			reload.leftPanel = true;
			jailPowerSignal.token += 1;
			jailPowerSignal.ctId = targetCTID;
			jailPowerSignal.action = 'stop';

			toast.success('Jail stop queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Error stopping jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} finally {
			await Promise.allSettled([jail.refetch(), jState.refetch()]);
			actionRequestInFlight = false;
		}
	}

	async function handleStart() {
		if (!jail.current || actionRequestInFlight) return;
		const targetCTID = jail.current.ctId;
		actionRequestInFlight = true;

		try {
			const result = await jailAction(targetCTID, 'start', node);
			if (isAPIResponse(result)) {
				toast.error(
					result.message === 'lifecycle_task_in_progress' ||
						result.message === 'migration_in_progress'
						? 'Jail action already in progress'
						: 'Error starting jail',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
				return;
			}

			reload.leftPanel = true;
			jailPowerSignal.token += 1;
			jailPowerSignal.ctId = targetCTID;
			jailPowerSignal.action = 'start';

			toast.success('Jail start queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Error starting jail', {
				duration: 5000,
				position: 'bottom-center'
			});
		} finally {
			await Promise.allSettled([jail.refetch(), jState.refetch()]);
			actionRequestInFlight = false;
		}
	}

	let jailModalConfirmationState = $state(false);

	function openStopConfirmationModal() {
		if (!jail.current) return;
		jailModalConfirmationState = true;
	}

	function handleConfirmation() {
		if (!jail.current) return;
		handleStop();
	}
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	<div class="flex h-10 w-full shrink-0 items-center justify-between gap-1 border p-4">
		{#if !isSummaryPage}
			<div class="min-w-0 flex items-center gap-2">
				{#if jail.current && jState.current}
					<Badge
						variant="outline"
						class="text-muted-foreground px-1.5"
						title={jState.current.state}
					>
						{#if jState.current.state === 'ACTIVE'}
							<span class="icon-[mdi--check-circle] text-green-500"></span>
						{:else}
							<span class="icon-[mdi--close-circle] text-gray-500"></span>
						{/if}
					</Badge>
					<p class="truncate text-sm font-semibold">{jail.current.name} ({jail.current.ctId})</p>
				{/if}
			</div>
		{/if}

		<div class="flex items-center gap-1">
			{#if jail.current && jState.current}
				{#if !shouldHideActionButtons && jState.current.state === 'ACTIVE'}
					<Button
						onclick={() => openStopConfirmationModal()}
						disabled={actionRequestInFlight}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<SpanWithIcon icon="icon-[mdi--stop]" size="h-4 w-4" gap="gap-1" title="Stop" />
					</Button>
				{:else if !shouldHideActionButtons}
					<Button
						onclick={handleStart}
						disabled={actionRequestInFlight}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<SpanWithIcon icon="icon-[mdi--play]" size="h-4 w-4" gap="gap-1" title="Start" />
					</Button>

					<Button
						onclick={openDeleteModal}
						size="sm"
						class="ml-2 bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
					</Button>
				{/if}

				{#if hasActiveLifecycleTask && !isMigrationActive}
					<Badge
						variant={lifecycleActionBadge.variant}
						class={`px-1.5 text-xs ${lifecycleActionBadge.className}`}
					>
						<span class="icon-[mdi--loading] mr-1 h-3 w-3 animate-spin"></span>
						{lifecycleActionBadge.label}
					</Badge>
				{/if}
			{/if}
		</div>

		{#if isSummaryPage}
			<div class="ml-auto flex items-center gap-2">
				{#if summaryBarExtras.logsLength > 0}
					<Button
						size="sm"
						onclick={summaryBarExtras.showLogsCallback}
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
					>
						<SpanWithIcon
							icon="icon-[mdi--file-document-outline]"
							size="h-4 w-4"
							gap="gap-1"
							title="View Logs"
						/>
					</Button>
				{/if}

				{#if availableNodeCount > 0}
					<Button
						onclick={() => (showMigrateModal = true)}
						disabled={actionRequestInFlight || (hasActiveLifecycleTask && !isMigrationActive)}
						size="sm"
						class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-purple-600 disabled:hover:bg-neutral-600 dark:text-white"
					>
						{#if isMigrationActive}
							<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin text-purple-500"></span>
						{:else}
							<span class="icon-[mdi--swap-horizontal] mr-1 h-4 w-4"></span>
						{/if}
						<span>Migrate</span>
					</Button>
				{/if}
				<SimpleSelect
					options={[
						{ label: 'Hourly', value: 'hourly' },
						{ label: 'Daily', value: 'daily' },
						{ label: 'Weekly', value: 'weekly' },
						{ label: 'Monthly', value: 'monthly' },
						{ label: 'Yearly', value: 'yearly' }
					]}
					bind:value={summaryBarExtras.gfsStep}
					onChange={(value) => summaryBarExtras.refetchStats(value as GFSStep)}
					classes={{ trigger: 'h-6!' }}
					icon="icon-[mdi--calendar]"
				/>
			</div>
		{/if}
	</div>

	<div
		class="min-h-0 flex-1"
		class:overflow-hidden={isConsolePage}
		class:overflow-auto={!isConsolePage}
	>
		{#key `${node}:${ctId}`}
			{@render children?.()}
		{/key}
	</div>
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title>Are you sure?</AlertDialogRaw.Title>
			<AlertDialogRaw.Description>
				This will permanently delete the jail
				<span class="font-semibold">{modalState?.title}.</span>
				<div class="flex flex-row gap-2">
					<CustomCheckbox
						label="Delete MAC Object(s)"
						bind:checked={modalState.deleteMacs}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>
					<CustomCheckbox
						label="Delete Root Filesystem"
						bind:checked={modalState.deleteRootFS}
						classes="flex items-center gap-2 mt-4"
					></CustomCheckbox>
				</div>
				{#if !modalState.deleteRootFS}
					<div
						class="mt-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
					>
						<span
							class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400"
							aria-hidden="true"
						></span>
						<div>
							<p class="font-medium text-amber-600 dark:text-amber-400">Root filesystem retained</p>
							<p class="mt-0.5 text-muted-foreground">
								The root filesystem will remain as unmanaged ZFS data. Remove it before reusing this
								CTID.
							</p>
						</div>
					</div>
				{/if}
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}>Continue</AlertDialogRaw.Action>
		</AlertDialogRaw.Footer>
	</AlertDialogRaw.Content>
</AlertDialogRaw.Root>

<AlertDialogRaw.Root bind:open={jailModalConfirmationState}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5 max-w-xl!">
		<AlertDialogRaw.Header>STOP the running Jail?</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					jailModalConfirmationState = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action
				onclick={() => {
					handleConfirmation();
					jailModalConfirmationState = false;
				}}>Confirm</AlertDialogRaw.Action
			>
		</AlertDialogRaw.Footer>
	</AlertDialogRaw.Content>
</AlertDialogRaw.Root>

<LoadingDialog
	bind:open={modalState.loading.open}
	title={modalState.loading.title}
	description={modalState.loading.description}
	iconColor={modalState.loading.iconColor}
/>

<MigrateModal
	bind:open={showMigrateModal}
	guestType="jail"
	guestId={ctId}
	guestName={jail.current?.name || ''}
	node={page.params.node || ''}
	{sourceNodeUuid}
	onSuccess={(targetHostname: string) => {
		if (targetHostname) {
			useSafeGoto(
				resolve('/[node]/jail/[ctid]/summary', {
					node: targetHostname,
					ctid: String(ctId)
				})
			);
		}
	}}
/>
