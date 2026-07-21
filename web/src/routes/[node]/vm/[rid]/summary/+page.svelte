<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import {
		actionVm,
		deleteVM,
		getStats,
		getVmById,
		getVMLogs,
		purgeVMRegistration,
		updateDescription,
		updateName
	} from '$lib/api/vm/vm';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import {
		VMStatSchema,
		type QGAInfo,
		type VM,
		type VMDomain,
		type VMStat
	} from '$lib/types/vm/vm';
	import { getObjectSchemaDefaults, sleep } from '$lib/utils';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { dateToAgo } from '$lib/utils/time';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { resource, useInterval, Debounced, IsDocumentVisible, watch } from 'runed';
	import { getContext } from 'svelte';
	import { parseGuestDeletionData, type APIResponse, type GFSStep } from '$lib/types/common';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import {
		getVMIconByGaId,
		removeStaleCacheByRID,
		getVMLifecycleBadgeStyle
	} from '$lib/utils/vm/vm';
	import GuestAgent from '$lib/components/custom/VM/Summary/GuestAgent.svelte';
	import Badge from '$lib/components/ui/badge/badge.svelte';
	import { resolve } from '$app/paths';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import MigrateModal from '$lib/components/custom/Vm/MigrateModal.svelte';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import type { ClusterNode } from '$lib/types/cluster/cluster';

	interface Data {
		node: string;
		rid: number;
		vm: VM;
		stats: VMStat[];
		gaInfo: QGAInfo | APIResponse | null;
		nodes: ClusterNode[] | null;
	}

	let { data }: { data: Data } = $props();
	let gfsStep = $state<GFSStep>('hourly');

	let sourceNodeUuid = $derived.by(() => {
		const nds = data.nodes;
		if (!nds || !Array.isArray(nds)) return '';
		const self = nds.find((n) => n.guestIDs?.includes(data.rid));
		if (self) return self.nodeUUID;
		const selfNode = data.node.toLowerCase();
		const byName = nds.find((n) => n.hostname.toLowerCase() === selfNode);
		return byName?.nodeUUID ?? '';
	});

	let availableNodeCount = $derived.by(() => {
		const nds = data.nodes;
		if (!nds || !Array.isArray(nds)) return 1;
		const selfUuid = sourceNodeUuid;
		const selfHostname = data.node.toLowerCase();
		let count = 0;
		for (const n of nds) {
			if (n.nodeUUID === '' || n.status !== 'online') continue;
			if (n.nodeUUID === selfUuid) continue;
			if (!selfUuid && n.hostname.toLowerCase() === selfHostname) continue;
			count++;
		}
		return count;
	});

	// svelte-ignore state_referenced_locally
	const vm = resource(
		() => 'vm-' + data.rid,
		async (key) => {
			const result = await getVmById(Number(data.rid), 'rid');
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.vm }
	);

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	const logs = resource(
		() => `vm-${data.rid}-logs`,
		async (key) => {
			const result = await getVMLogs(Number(data.rid));
			updateCache(key, result);
			return result;
		},
		{ initialValue: { logs: '' } }
	);

	// svelte-ignore state_referenced_locally
	const stats = resource(
		[() => gfsStep],
		async ([gfsStep]) => {
			const result = await getStats(Number(data.vm.rid), gfsStep);
			const key = `vm-stats-${data.vm.rid}`;
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.stats }
	);

	const visible = new IsDocumentVisible();

	useInterval(() => 3000, {
		callback: () => {
			if (visible.current && !isDeleteInFlight) {
				if (gfsStep === 'hourly') {
					stats.refetch();
				}
			}
		}
	});

	useInterval(() => 3000, {
		callback: () => {
			if (visible.current && showLogs) {
				logs.refetch();
			}
		}
	});

	watch(
		() => domain.current,
		(currentDomain, prevDomain) => {
			if (prevDomain?.status !== currentDomain?.status) {
				vm.refetch();
			}
		}
	);

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle && !isDeleteInFlight) {
				vm.refetch();
				stats.refetch();
			}

			if (!idle && showLogs) {
				logs.refetch();
			}
		}
	);

	let recentStat = $derived(
		stats.current[stats.current.length - 1] || getObjectSchemaDefaults(VMStatSchema)
	);
	let gaRefreshSignal = $state(0);
	let isQgaEnabled = $derived.by(() => vm.current?.qemuGuestAgent === true);
	let initialGaInfo = $derived.by(() => {
		if (!isQgaEnabled || !data.gaInfo || isAPIResponse(data.gaInfo)) {
			return null;
		}

		return data.gaInfo;
	});
	let showLogs = $state(false);
	let logsContainerElement = $state<HTMLDivElement | null>(null);
	let followLogs = $state(true);
	let vmLogs = $derived.by(() => {
		const currentLogs = logs.current?.logs;
		return typeof currentLogs === 'string' ? currentLogs : '';
	});
	const LOG_AUTO_SCROLL_THRESHOLD = 24;

	let vmDescription = $state(vm.current.description || '');
	let debouncedDesc = new Debounced(() => vmDescription, 500);
	let isDescInitialized = false;
	let vmName = $state(vm.current.name || '');
	let syncedVMName = $state(vm.current.name || '');
	let isRenameInFlight = $state(false);
	let isEditingName = $state(false);

	function startEditingName() {
		isEditingName = true;
	}

	function cancelEditingName() {
		vmName = syncedVMName;
		isEditingName = false;
	}

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);
	let isDomainErrorState = $derived(normalizedDomainStatus === 'error');
	let isOrphanState = $derived(normalizedDomainStatus === 'orphan');
	let hasActiveLifecycleTask = $derived(!!domain.current?.pendingAction);
	let isMigrationActive = $derived(domain.current?.pendingAction === 'migrate');
	let lifecycleActionBadge = $derived(
		getVMLifecycleBadgeStyle(domain.current?.pendingAction || '')
	);
	let shouldHideActionButtons = $derived(hasActiveLifecycleTask && !isMigrationActive);
	let showMigrateModal = $state(false);
	let isDomainRunningForActions = $derived.by(() => {
		if (normalizedDomainStatus === 'running') return true;
		if (isDomainErrorState) return false;
		const pending = domain.current?.pendingAction;
		if (pending === 'start' || pending === 'reboot') return true;
		return false;
	});
	let isShutdownTaskActive = $derived(
		domain.current?.pendingAction === 'shutdown' && !domain.current?.overrideRequested
	);

	let isDeleteInFlight = $state(false);

	watch(
		() => debouncedDesc.current,
		(curr, prev) => {
			if (!isDescInitialized) {
				isDescInitialized = true;
				return;
			}

			if (curr !== undefined && prev !== undefined) {
				if (curr !== prev) {
					updateDescription(data.rid, curr);
				}
			}
		}
	);

	watch(
		() => vm.current.name,
		(currentName) => {
			const normalized = currentName || '';
			if (!isRenameInFlight && vmName === syncedVMName) {
				vmName = normalized;
			}
			syncedVMName = normalized;
		}
	);

	let modalState = $state({
		isDeleteOpen: false,
		forceDelete: false,
		purgeOnly: false,
		deleteMACs: true,
		deleteRAWDisks: false,
		deleteVolumes: false,
		title: '',
		loading: {
			open: false,
			title: '',
			description: '',
			iconColor: ''
		}
	});

	function openDeleteModal(forceDelete: boolean = false) {
		modalState.purgeOnly = false;
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	function openRemoveModal() {
		modalState.purgeOnly = true;
		modalState.forceDelete = false;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = false;
		modalState.deleteVolumes = false;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	function isNearLogsBottom(element: HTMLDivElement): boolean {
		return (
			element.scrollHeight - element.scrollTop - element.clientHeight <= LOG_AUTO_SCROLL_THRESHOLD
		);
	}

	function handleLogsScroll() {
		if (!logsContainerElement) {
			return;
		}

		followLogs = isNearLogsBottom(logsContainerElement);
	}

	async function refreshLifecycleState() {
		await Promise.all([domain.refetch(), vm.refetch()]);
	}

	async function handleDelete() {
		isDeleteInFlight = true;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = modalState.purgeOnly
			? 'Removing Stale VM Entry'
			: modalState.forceDelete
				? 'Force Deleting Virtual Machine'
				: 'Deleting Virtual Machine';
		modalState.loading.description = modalState.purgeOnly
			? `Removing stale registration for VM <b>${vm.current.name} (${vm.current.rid})</b>; datasets are preserved`
			: modalState.forceDelete
				? `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being force deleted with best-effort cleanup`
				: `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being deleted`;

		await sleep(1000);
		const result = modalState.purgeOnly
			? await purgeVMRegistration(vm.current.rid, modalState.deleteMACs)
			: await deleteVM(
					vm.current.rid,
					modalState.deleteMACs,
					modalState.deleteRAWDisks,
					modalState.deleteVolumes,
					modalState.forceDelete
				);
		modalState.loading.open = false;
		reload.leftPanel = true;
		const wasForceDelete = modalState.forceDelete;
		const wasPurgeOnly = modalState.purgeOnly;
		modalState.forceDelete = false;
		modalState.purgeOnly = false;

		if (result.status === 'error') {
			isDeleteInFlight = false;
			await Promise.all([vm.refetch(), domain.refetch(), stats.refetch()]);
			toast.error(
				result.message === 'guest_delete_requires_replication_policy_removed'
					? 'Remove the replication policy before deleting this VM'
					: wasPurgeOnly
						? 'Error removing VM entry'
						: wasForceDelete
							? 'Error force deleting VM'
							: 'Error deleting VM',
				{ duration: 5000, position: 'bottom-center' }
			);
		} else if (result.status === 'success') {
			const deletionData = parseGuestDeletionData(result.data);
			const cleanupWarnings = deletionData.warnings;
			const retainedDatasets = deletionData.retainedDatasets;
			await useSafeGoto(
				resolve('/[node]/summary', {
					node: data.node
				})
			);
			if (wasPurgeOnly && result.message === 'vm_registration_purged_with_warnings') {
				toast.warning('VM entry removed with warnings; datasets preserved', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else if (wasPurgeOnly) {
				toast.success('VM entry removed (datasets preserved)', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else if (wasForceDelete && result.message === 'vm_force_removed_with_warnings') {
				toast.warning('VM force deleted with warnings', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else if (!wasForceDelete && cleanupWarnings.length > 0) {
				toast.warning(
					`VM deleted, but cleanup was incomplete${retainedDatasets.length > 0 ? `: ${retainedDatasets.join(', ')}` : ''}`,
					{ duration: 8000, position: 'bottom-center' }
				);
			} else if (!wasForceDelete && retainedDatasets.length > 0) {
				toast.warning(`VM deleted; storage retained at ${retainedDatasets.join(', ')}`, {
					duration: 8000,
					position: 'bottom-center'
				});
			} else {
				toast.success(wasForceDelete ? 'VM force deleted' : 'VM deleted', {
					duration: 5000,
					position: 'bottom-center'
				});
			}

			removeStaleCacheByRID(vm.current.rid);
		}
	}

	let isVMNameDirty = $derived.by(() => vmName.trim() !== String(vm.current.name || '').trim());

	let canSaveVMName = $derived.by(() => !isRenameInFlight && vmName.trim() !== '' && isVMNameDirty);

	async function handleRename() {
		const nextName = vmName.trim();
		const currentName = String(vm.current.name || '').trim();

		if (!nextName || nextName === currentName || isRenameInFlight) {
			return;
		}

		isRenameInFlight = true;
		const result = await updateName(vm.current.rid, nextName);
		if (result.status === 'success') {
			isEditingName = false;
			reload.leftPanel = true;
			await vm.refetch();
			vmName = vm.current.name || nextName;
			syncedVMName = vmName;
			toast.success('VM name updated', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else {
			let errorMessage = 'Error updating VM name';
			if (result.message === 'invalid_vm_name') {
				errorMessage = 'Invalid VM name. Use letters, numbers, - or _.';
			} else if (result.message === 'vm_name_already_in_use') {
				errorMessage = 'VM name is already in use';
			} else if (result.message === 'replication_lease_not_owned') {
				errorMessage = 'This VM is owned by another node right now';
			}

			toast.error(errorMessage, {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		isRenameInFlight = false;
	}

	async function handleStart() {
		const result = await actionVm(vm.current.rid, 'start');
		domain.refetch();
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				toast.error(
					result.message === 'lifecycle_task_in_progress'
						? 'VM action already in progress'
						: 'Error starting VM',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
			}
		} else {
			if (result.outcome === 'queued') {
				toast.success('VM start queued', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		}

		gaRefreshSignal += 1;
		await refreshLifecycleState();
	}

	async function handleStop() {
		const result = await actionVm(vm.current.rid, 'stop');
		domain.refetch();
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				toast.error(
					result.message === 'lifecycle_task_in_progress'
						? 'VM action already in progress'
						: 'Error stopping VM',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
			} else if (result.status === 'success') {
				if (result.message === 'vm_force_stop_requested') {
					toast.warning('Force stop requested', {
						duration: 5000,
						position: 'bottom-center'
					});
				}
			}
		} else if (result.outcome === 'queued') {
			toast.success('VM stop queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshLifecycleState();
	}

	async function handleForceStop() {
		const result = await actionVm(vm.current.rid, 'stop');
		domain.refetch();
		reload.leftPanel = true;

		if (isAPIResponse(result) && result.status === 'error') {
			toast.error(
				result.message === 'lifecycle_task_in_progress'
					? 'VM action already in progress'
					: 'Error requesting force stop',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			toast.warning('Force stop requested', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshLifecycleState();
	}

	async function handleShutdown() {
		const result = await actionVm(vm.current.rid, 'shutdown');
		domain.refetch();
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				toast.error(
					result.message === 'lifecycle_task_in_progress'
						? 'VM action already in progress'
						: 'Error shutting down VM',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
			} else if (result.status === 'success') {
				toast.success('VM shutdown queued', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		} else if (result.outcome === 'queued') {
			toast.success('VM shutdown queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshLifecycleState();
	}

	async function handleReboot() {
		const result = await actionVm(vm.current.rid, 'reboot');
		domain.refetch();
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				toast.error(
					result.message === 'lifecycle_task_in_progress'
						? 'VM action already in progress'
						: 'Error rebooting VM',
					{
						duration: 5000,
						position: 'bottom-center'
					}
				);
			} else if (result.status === 'success') {
				toast.success('VM reboot queued', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		} else if (result.outcome === 'queued') {
			toast.success('VM reboot queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		gaRefreshSignal += 1;
		await refreshLifecycleState();
	}

	watch(
		() => vmLogs,
		() => {
			if (showLogs && followLogs) {
				if (logsContainerElement) {
					logsContainerElement.scrollTop = logsContainerElement.scrollHeight;
				}
			}
		}
	);

	watch(
		() => showLogs,
		(show) => {
			if (show) {
				followLogs = true;
				requestAnimationFrame(() => {
					if (logsContainerElement) {
						logsContainerElement.scrollTop = logsContainerElement.scrollHeight;
					}
				});
			}
		}
	);

	let udTime = $derived.by(() => {
		if (domain.current?.status === 'Running') {
			if (vm.current.startedAt) {
				return `Started ${dateToAgo(vm.current.startedAt)}`;
			}
		} else if (domain.current?.status === 'Stopped' || domain.current?.status === 'Shutoff') {
			if (vm.current.stoppedAt) {
				return `Stopped ${dateToAgo(vm.current.stoppedAt)}`;
			}
		}
		return '';
	});
</script>

{#snippet button(type: string)}
	{#if type === 'start' && !shouldHideActionButtons && domain.current?.id == -1 && !isDomainRunningForActions && !isDomainErrorState && !isOrphanState}
		<Button
			onclick={() => handleStart()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon icon="icon-[mdi--play]" size="h-4 w-4" gap="gap-1" title="Start" />
		</Button>

		<Button
			onclick={() => openDeleteModal(false)}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
		</Button>
	{:else if type === 'force-delete' && !shouldHideActionButtons && isDomainErrorState}
		<Button
			onclick={() => openDeleteModal(true)}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-700 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon
				icon="icon-[mdi--alert-octagon]"
				size="h-4 w-4"
				gap="gap-1"
				title="Force Delete"
			/>
		</Button>
	{:else if type === 'remove-orphan' && !shouldHideActionButtons && isOrphanState}
		<Button
			onclick={() => openRemoveModal()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon
				icon="icon-[mdi--delete-sweep]"
				size="h-4 w-4"
				gap="gap-1"
				title="Remove stale entry"
			/>
		</Button>
	{:else if type === 'force-stop' && (domain.current?.id !== -1 || domain.current?.pendingAction === 'start' || domain.current?.pendingAction === 'reboot') && isDomainRunningForActions && isShutdownTaskActive}
		<Button
			onclick={() => handleForceStop()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon icon="icon-[mdi--alert]" size="h-4 w-4" gap="gap-1" title="Force Stop" />
		</Button>
	{:else if (type === 'stop' || type === 'shutdown' || type === 'reboot') && !shouldHideActionButtons && (domain.current?.id !== -1 || domain.current?.pendingAction === 'start' || domain.current?.pendingAction === 'reboot') && isDomainRunningForActions}
		<Button
			onclick={() =>
				type === 'stop' ? handleStop() : type === 'shutdown' ? handleShutdown() : handleReboot()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<SpanWithIcon
				icon={type === 'stop'
					? 'icon-[mdi--stop]'
					: type === 'shutdown'
						? 'icon-[mdi--power]'
						: 'icon-[mdi--restart]'}
				size="h-4 w-4"
				gap="gap-1"
				title={type === 'stop' ? 'Stop' : type === 'shutdown' ? 'Shutdown' : 'Reboot'}
			/>
		</Button>
	{:else if type === 'migrate' && !isOrphanState}
		<Button
			onclick={() => (showMigrateModal = true)}
			disabled={isMigrationActive}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-purple-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center gap-1">
				{#if isMigrationActive}
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin text-purple-500"></span>
				{:else}
					<span class="icon-[mdi--swap-horizontal] h-4 w-4"></span>
				{/if}
				<span>Migrate</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div>
	<div class="flex h-10 w-full items-center gap-1 border p-4">
		<div class="flex items-center gap-1">
			{#if !isMigrationActive}
				{@render button('start')}
				{@render button('force-delete')}
				{@render button('remove-orphan')}
				{@render button('force-stop')}
				{@render button('reboot')}
				{@render button('shutdown')}
				{@render button('stop')}
			{/if}

			{#if hasActiveLifecycleTask && !isMigrationActive}
				<Badge
					variant={lifecycleActionBadge.variant}
					class={`mt-0.5 ml-2 ${lifecycleActionBadge.className}`}
				>
					<span class="icon-[mdi--loading] mr-1 h-3 w-3 animate-spin"></span>
					{lifecycleActionBadge.label}
				</Badge>
			{/if}
		</div>

		<div class="ml-auto flex h-full items-center gap-2">
			{#if vmLogs.length > 0}
				<div>
					<Button
						size="sm"
						onclick={() => {
							followLogs = true;
							showLogs = true;
							logs.refetch();
						}}
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
					>
						<SpanWithIcon
							icon="icon-[mdi--file-document-outline]"
							size="h-4 w-4"
							gap="gap-1"
							title="View Logs"
						/>
					</Button>
				</div>
			{/if}

			{#if availableNodeCount > 0}
				{@render button('migrate')}
			{/if}

			<SimpleSelect
				options={[
					{ label: 'Hourly', value: 'hourly' },
					{ label: 'Daily', value: 'daily' },
					{ label: 'Weekly', value: 'weekly' },
					{ label: 'Monthly', value: 'monthly' },
					{ label: 'Yearly', value: 'yearly' }
				]}
				bind:value={gfsStep}
				onChange={() => {
					stats.refetch();
				}}
				classes={{ trigger: 'h-6!' }}
				icon="icon-[mdi--calendar]"
			/>
		</div>
	</div>

	<div class="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
		<Card.Root class="w-full gap-0 p-4">
			<Card.Header class="p-0">
				<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
					<div class="group flex items-center gap-1.5">
						{#if isQgaEnabled && initialGaInfo && getVMIconByGaId(initialGaInfo.osInfo.id || '')}
							<span class="icon {getVMIconByGaId(initialGaInfo.osInfo.id || '')} h-6 w-6 shrink-0"
							></span>
						{/if}
						{#if isEditingName}
							<!-- svelte-ignore a11y_autofocus -->
							<input
								class="border-b border-current bg-transparent outline-none min-w-0 w-40"
								bind:value={vmName}
								onkeydown={(e) => {
									if (e.key === 'Enter' && canSaveVMName) handleRename();
									if (e.key === 'Escape') cancelEditingName();
								}}
								autofocus
							/>
							<button
								onclick={handleRename}
								disabled={!canSaveVMName || isRenameInFlight}
								class="text-current disabled:opacity-40"
								title="Save"
							>
								<span class="icon-[mdi--check] h-5 w-5"></span>
							</button>
							<button onclick={cancelEditingName} class="text-current" title="Cancel">
								<span class="icon-[mdi--close] h-5 w-5"></span>
							</button>
						{:else}
							<span class="whitespace-nowrap">{vm.current.name}</span>
							{#if udTime}
								<span class="whitespace-nowrap">({udTime})</span>
							{/if}
							<button
								onclick={startEditingName}
								class="invisible group-hover:visible transition-opacity text-current"
								title="Edit Name"
							>
								<span class="icon-[mdi--pencil] h-3.5 w-3.5"></span>
							</button>
						{/if}
					</div>
				</Card.Description>
			</Card.Header>
			<Card.Content class="mt-3 p-0">
				<div class="flex items-start">
					<div class="flex items-center">
						<span class="icon-[fluent--status-12-filled] mr-1 h-5 w-5"></span>
						<span>Status</span>
					</div>
					<div class="ml-auto">
						{domain.current?.status}
					</div>
				</div>

				<div class="mt-2">
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>
							<span>CPU Usage</span>
						</p>
						<p class="ml-auto">
							{#if domain.current?.status === 'Running'}
								{`${floatToNDecimals(recentStat.cpuUsage, 2)}% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
							{:else}
								{`0% of ${vm.current.cpuCores * vm.current.cpuThreads * vm.current.cpuSockets} vCPU(s)`}
							{/if}
						</p>
					</div>

					{#if domain.current?.status === 'Running'}
						<Progress value={recentStat.cpuUsage || 0} max={100} class="ml-auto h-2" />
					{:else}
						<Progress value={0} max={100} class="ml-auto h-2" />
					{/if}
				</div>

				<div class="mt-2">
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[ph--memory] mr-1 h-5 w-5"></span>
							<span>RAM Usage</span>
						</p>
						<p class="ml-auto">
							{#if vm}
								{#if domain.current?.status === 'Running'}
									{`${floatToNDecimals(recentStat.memoryUsage, 2)}% of ${formatBytesBinary(vm.current.ram || 0)}`}
								{:else}
									{`0% of ${formatBytesBinary(vm.current.ram || 0)}`}
								{/if}
							{/if}
						</p>
					</div>

					{#if domain.current?.status === 'Running'}
						<Progress value={recentStat.memoryUsage || 0} max={100} class="ml-auto h-2" />
					{:else}
						<Progress value={0} max={100} class="ml-auto h-2" />
					{/if}
				</div>
			</Card.Content>
		</Card.Root>

		<Card.Root class="w-full gap-0 p-4">
			<Card.Header class="p-0">
				<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
					Description
				</Card.Description>
			</Card.Header>
			<Card.Content class="mt-3 p-0">
				<CustomValueInput
					label=""
					placeholder="Notes about VM"
					bind:value={vmDescription}
					classes=""
					textAreaClasses="!h-28"
					type="textarea"
				/>
			</Card.Content>
		</Card.Root>
	</div>

	<GuestAgent
		rid={data.vm.rid}
		initialGaInfo={data.gaInfo}
		refreshSignal={gaRefreshSignal}
		qgaEnabled={isQgaEnabled}
	/>

	<div class="space-y-4 px-4 pb-4">
		<LineBrush
			title="CPU Usage"
			points={stats.current.map((data) => ({
				date: new Date(data.createdAt).getTime(),
				value: Number(data.cpuUsage)
			}))}
			percentage={true}
			color="one"
			containerContentHeight="h-64"
			titleIconClass="icon-[solar--cpu-bold]"
		/>

		<LineBrush
			title="Memory Usage"
			points={stats.current.map((data) => ({
				date: new Date(data.createdAt).getTime(),
				value: Number(data.memoryUsage)
			}))}
			percentage={true}
			color="two"
			containerContentHeight="h-64"
			titleIconClass="icon-[ph--memory]"
		/>
	</div>
</div>

<AlertDialogRaw.Root bind:open={modalState.isDeleteOpen}>
	<AlertDialogRaw.Content onInteractOutside={(e) => e.preventDefault()} class="p-5 max-w-xl!">
		<AlertDialogRaw.Header>
			<AlertDialogRaw.Title
				>{modalState.purgeOnly
					? 'Remove stale VM entry?'
					: modalState.forceDelete
						? 'Force Delete VM?'
						: 'Are you sure?'}</AlertDialogRaw.Title
			>
			<AlertDialogRaw.Description>
				{#if modalState.purgeOnly}
					This will remove the stale inventory entry for VM
					<span class="font-semibold">{modalState?.title}</span> on this node.
					<div class="mt-2 text-sm">
						Only the local VM record and any local libvirt domain are removed. ZFS datasets are
						preserved and nothing is deleted on other nodes.
					</div>
				{:else}
					{modalState.forceDelete ? `This will force delete VM` : `This will permanently delete VM`}
					<span class="font-semibold">{modalState?.title}.</span>
					{#if modalState.forceDelete}
						<div class="mt-2 text-sm">
							Best-effort cleanup will attempt libvirt/domain removal, VM datasets, VM DB records,
							and VM network objects. Partial failures will be tolerated.
						</div>
					{:else}
						<div class="flex flex-row items-center gap-6 mt-1 whitespace-nowrap">
							<CustomCheckbox
								label="Delete MAC Object(s)"
								bind:checked={modalState.deleteMACs}
								classes="flex items-center gap-2 mt-3"
							></CustomCheckbox>

							<CustomCheckbox
								label="Delete RAW Disk(s)"
								bind:checked={modalState.deleteRAWDisks}
								classes="flex items-center gap-2 mt-3"
							></CustomCheckbox>

							<CustomCheckbox
								label="Delete Volume(s)"
								bind:checked={modalState.deleteVolumes}
								classes="flex items-center gap-2 mt-3"
							></CustomCheckbox>
						</div>
						{#if !modalState.deleteRAWDisks || !modalState.deleteVolumes}
							<div
								class="mt-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
							>
								<span
									class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400"
									aria-hidden="true"
								></span>
								<div>
									<p class="font-medium text-amber-600 dark:text-amber-400">Storage retained</p>
									<p class="mt-0.5 text-muted-foreground">
										Unselected storage will remain as unmanaged ZFS data. Remove it before reusing
										this RID.
									</p>
								</div>
							</div>
						{/if}
					{/if}
				{/if}
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
					modalState.forceDelete = false;
					modalState.purgeOnly = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}
				>{modalState.purgeOnly
					? 'Remove'
					: modalState.forceDelete
						? 'Force Delete'
						: 'Continue'}</AlertDialogRaw.Action
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

<Dialog.Root bind:open={showLogs}>
	<Dialog.Content
		class="min-w-3xl overflow-hidden"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		showCloseButton={true}
		onClose={() => {
			showLogs = false;
		}}
	>
		<Dialog.Header class="flex w-full min-w-0 flex-col">
			<Dialog.Title class="flex justify-between text-left">
				<div class="flex items-center gap-2">
					<SpanWithIcon
						icon="icon-[material-symbols--terminal]"
						size="h-6 w-6"
						gap="gap-2"
						title={`${vm.current.name} Logs`}
					/>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<Card.Root class="w-full min-w-0 gap-0 bg-black p-4 dark:bg-black">
			<Card.Content class="mt-3 w-full min-w-0 max-w-full p-0">
				<div
					class="logs-container max-h-64 w-full overflow-x-auto overflow-y-auto"
					bind:this={logsContainerElement}
					onscroll={handleLogsScroll}
				>
					<pre class="block min-w-0 whitespace-pre text-xs text-[#4AF626]">
						{vmLogs}
					</pre>
				</div>
			</Card.Content>
		</Card.Root>
	</Dialog.Content>
</Dialog.Root>

<MigrateModal
	bind:open={showMigrateModal}
	guestType="vm"
	guestId={Number(data.rid)}
	guestName={vm.current.name || ''}
	node={data.node}
	{sourceNodeUuid}
	onSuccess={(targetHostname: string) => {
		if (targetHostname) {
			goto(`/${targetHostname}/vm/${data.rid}/summary`);
		}
	}}
/>

<style>
	.logs-container {
		overflow: auto;
		scrollbar-width: none;
		-ms-overflow-style: none;
	}

	.logs-container::-webkit-scrollbar {
		display: none;
	}
</style>
