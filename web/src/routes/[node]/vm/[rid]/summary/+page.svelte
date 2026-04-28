<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card/index.js';
	import {
		actionVm,
		deleteVM,
		getStats,
		getVmById,
		getVMLogs,
		updateDescription,
		updateName
	} from '$lib/api/vm/vm';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import {
		VMStatSchema,
		type QGAInfo,
		type VM,
		type VMLifecycleAction,
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
	import type { APIResponse, GFSStep } from '$lib/types/common';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import {
		createVMPendingLifecycleSnapshot,
		getEffectiveVMLifecycleAction,
		getVMIconByGaId,
		getVMLifecyclePendingTimeoutMs,
		getVMLifecycleBadgeStyle,
		isVMPendingLifecycleActionSettled,
		isVMLifecycleTransitionPending,
		markVMPendingSnapshotNonRunning,
		shouldHideVMLifecycleButtons,
		removeStaleCacheByRID,
		type VMPendingLifecycleSnapshot
	} from '$lib/utils/vm/vm';
	import GuestAgent from '$lib/components/custom/VM/Summary/GuestAgent.svelte';
	import { fade } from 'svelte/transition';
	import Badge from '$lib/components/ui/badge/badge.svelte';
	import { resolve } from '$app/paths';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Data {
		node: string;
		rid: number;
		vm: VM;
		stats: VMStat[];
		gaInfo: QGAInfo | APIResponse | null;
	}

	let { data }: { data: Data } = $props();
	let gfsStep = $state<GFSStep>('hourly');

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
	const lifecycleTask = getContext<{ current: LifecycleTask | null; refetch(): void }>(
		'vmLifecycleTask'
	);

	const logs = resource(
		() => `vm-${data.rid}-logs`,
		async (key) => {
			const result = await getVMLogs(vm.current.rid);
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

	const NON_RUNNING_ACTION_SUPPRESS_MS = 1800;

	let pendingLifecycleAction = $state<VMLifecycleAction | ''>('');
	let pendingLifecycleSnapshot = $state<VMPendingLifecycleSnapshot | null>(null);
	let pendingLifecycleTimer: ReturnType<typeof setTimeout> | null = null;
	let suppressNonRunningActions = $state(false);
	let suppressNonRunningActionsTimer: ReturnType<typeof setTimeout> | null = null;
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
					updateDescription(vm.current.rid, curr);
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
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
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

	function beginNonRunningActionsSuppression() {
		suppressNonRunningActions = true;
		if (suppressNonRunningActionsTimer) {
			clearTimeout(suppressNonRunningActionsTimer);
		}

		suppressNonRunningActionsTimer = setTimeout(() => {
			suppressNonRunningActions = false;
			suppressNonRunningActionsTimer = null;
		}, NON_RUNNING_ACTION_SUPPRESS_MS);
	}

	function clearNonRunningActionsSuppression() {
		suppressNonRunningActions = false;
		if (suppressNonRunningActionsTimer) {
			clearTimeout(suppressNonRunningActionsTimer);
			suppressNonRunningActionsTimer = null;
		}
	}

	function beginPendingLifecycleAction(action: VMLifecycleAction) {
		pendingLifecycleAction = action;
		pendingLifecycleSnapshot = createVMPendingLifecycleSnapshot(
			String(domain.current?.status || ''),
			vm.current.startedAt ?? null
		);
		if (action === 'start' || action === 'reboot') {
			beginNonRunningActionsSuppression();
		} else {
			clearNonRunningActionsSuppression();
		}

		if (pendingLifecycleTimer) {
			clearTimeout(pendingLifecycleTimer);
		}

		// Safety net: never keep UI locked indefinitely if lifecycle polling misses an update.
		pendingLifecycleTimer = setTimeout(() => {
			pendingLifecycleAction = '';
			pendingLifecycleSnapshot = null;
			pendingLifecycleTimer = null;
		}, getVMLifecyclePendingTimeoutMs(action));
	}

	function clearPendingLifecycleAction() {
		pendingLifecycleAction = '';
		pendingLifecycleSnapshot = null;
		if (pendingLifecycleTimer) {
			clearTimeout(pendingLifecycleTimer);
			pendingLifecycleTimer = null;
		}
	}

	async function refreshLifecycleState() {
		await Promise.all([domain.refetch(), vm.refetch(), lifecycleTask.refetch()]);
	}

	async function handleDelete() {
		isDeleteInFlight = true;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = modalState.forceDelete
			? 'Force Deleting Virtual Machine'
			: 'Deleting Virtual Machine';
		modalState.loading.description = modalState.forceDelete
			? `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being force deleted with best-effort cleanup`
			: `Please wait while VM <b>${vm.current.name} (${vm.current.rid})</b> is being deleted`;

		await sleep(1000);
		const result = await deleteVM(
			vm.current.rid,
			modalState.deleteMACs,
			modalState.deleteRAWDisks,
			modalState.deleteVolumes,
			modalState.forceDelete
		);
		modalState.loading.open = false;
		reload.leftPanel = true;
		const wasForceDelete = modalState.forceDelete;
		modalState.forceDelete = false;

		if (result.status === 'error') {
			isDeleteInFlight = false;
			await Promise.all([vm.refetch(), domain.refetch(), stats.refetch(), lifecycleTask.refetch()]);
			toast.error(wasForceDelete ? 'Error force deleting VM' : 'Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			await goto(
				resolve('/[node]/summary', {
					node: data.node
				})
			);
			if (wasForceDelete && result.message === 'vm_force_removed_with_warnings') {
				toast.warning('VM force deleted with warnings', {
					duration: 5000,
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
		beginPendingLifecycleAction('start');
		const result = await actionVm(vm.current.rid, 'start');

		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				clearPendingLifecycleAction();
				clearNonRunningActionsSuppression();
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
		beginPendingLifecycleAction('stop');
		const result = await actionVm(vm.current.rid, 'stop');

		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				clearPendingLifecycleAction();
				clearNonRunningActionsSuppression();
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
		beginPendingLifecycleAction('stop');
		const result = await actionVm(vm.current.rid, 'stop');
		reload.leftPanel = true;

		if (isAPIResponse(result) && result.status === 'error') {
			clearPendingLifecycleAction();
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
		beginPendingLifecycleAction('shutdown');
		const result = await actionVm(vm.current.rid, 'shutdown');
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				clearPendingLifecycleAction();
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
		beginPendingLifecycleAction('reboot');
		const result = await actionVm(vm.current.rid, 'reboot');
		reload.leftPanel = true;

		if (isAPIResponse(result)) {
			if (result.status === 'error') {
				clearPendingLifecycleAction();
				clearNonRunningActionsSuppression();
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

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);
	let isDomainErrorState = $derived.by(() => normalizedDomainStatus === 'error');
	let hasLifecycleTaskRecord = $derived(!!lifecycleTask.current);
	let activeLifecycleAction = $derived(lifecycleTask.current?.action || '');
	let isActiveLifecycleActionSettled = $derived.by(() => {
		if (
			activeLifecycleAction !== 'start' &&
			activeLifecycleAction !== 'stop' &&
			activeLifecycleAction !== 'shutdown' &&
			activeLifecycleAction !== 'reboot'
		) {
			return false;
		}

		const snapshot =
			activeLifecycleAction === pendingLifecycleAction && pendingLifecycleSnapshot
				? pendingLifecycleSnapshot
				: createVMPendingLifecycleSnapshot(
						String(domain.current?.status || ''),
						vm.current.startedAt ?? null
					);

		return isVMPendingLifecycleActionSettled(
			activeLifecycleAction,
			snapshot,
			normalizedDomainStatus,
			isDomainErrorState,
			vm.current.startedAt ?? null
		);
	});
	let hasActiveLifecycleTask = $derived(hasLifecycleTaskRecord && !isActiveLifecycleActionSettled);
	let effectiveLifecycleAction = $derived(
		getEffectiveVMLifecycleAction(activeLifecycleAction, pendingLifecycleAction)
	);
	let isLifecycleTransitionPending = $derived(
		isVMLifecycleTransitionPending(pendingLifecycleAction, hasLifecycleTaskRecord)
	);
	let shouldHideActionButtons = $derived(
		shouldHideVMLifecycleButtons(hasActiveLifecycleTask, pendingLifecycleAction)
	);
	let isDomainRunningForActions = $derived.by(() => {
		if (normalizedDomainStatus === 'running') {
			return true;
		}

		if (!suppressNonRunningActions || isDomainErrorState) {
			return false;
		}

		if (
			pendingLifecycleAction === 'stop' ||
			pendingLifecycleAction === 'shutdown' ||
			activeLifecycleAction === 'stop' ||
			activeLifecycleAction === 'shutdown'
		) {
			return false;
		}

		return true;
	});
	let lifecycleActionBadge = $derived(getVMLifecycleBadgeStyle(effectiveLifecycleAction));
	let isShutdownTaskActive = $derived.by(
		() => lifecycleTask.current?.action === 'shutdown' && !lifecycleTask.current?.overrideRequested
	);

	watch(
		() => [pendingLifecycleAction, normalizedDomainStatus] as const,
		([pendingAction, currentStatus]) => {
			if (pendingAction !== 'reboot' || !pendingLifecycleSnapshot) {
				return;
			}

			const updatedSnapshot = markVMPendingSnapshotNonRunning(
				pendingLifecycleSnapshot,
				currentStatus,
				isDomainErrorState
			);
			if (updatedSnapshot !== pendingLifecycleSnapshot) {
				pendingLifecycleSnapshot = updatedSnapshot;
			}
		}
	);

	watch(
		() =>
			[
				pendingLifecycleAction,
				hasLifecycleTaskRecord,
				normalizedDomainStatus,
				vm.current.startedAt
			] as const,
		([pendingAction, hasTask]) => {
			if (!pendingAction || hasTask) {
				return;
			}

			if (
				isVMPendingLifecycleActionSettled(
					pendingAction,
					pendingLifecycleSnapshot,
					normalizedDomainStatus,
					isDomainErrorState,
					vm.current.startedAt ?? null
				)
			) {
				clearPendingLifecycleAction();
			}
		}
	);
</script>

{#snippet button(type: string)}
	{#if type === 'start' && !shouldHideActionButtons && domain.current?.id == -1 && !isDomainRunningForActions && !isDomainErrorState}
		<Button
			onclick={() => handleStart()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-green-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
			<span>Start</span>
		</Button>

		<Button
			onclick={() => openDeleteModal(false)}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
			<span>Delete</span>
		</Button>
	{:else if type === 'force-delete' && !shouldHideActionButtons && isDomainErrorState}
		<Button
			onclick={() => openDeleteModal(true)}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-700 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<span class="icon-[mdi--alert-octagon] mr-1 h-4 w-4"></span>
			<span>Force Delete</span>
		</Button>
	{:else if type === 'force-stop' && (domain.current?.id !== -1 || suppressNonRunningActions) && isDomainRunningForActions && isShutdownTaskActive}
		<Button
			onclick={() => handleForceStop()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--alert] mr-1 h-4 w-4"></span>
				<span>Force Stop</span>
			</div>
		</Button>
	{:else if (type === 'stop' || type === 'shutdown' || type === 'reboot') && !shouldHideActionButtons && (domain.current?.id !== -1 || suppressNonRunningActions) && isDomainRunningForActions}
		<Button
			onclick={() =>
				type === 'stop' ? handleStop() : type === 'shutdown' ? handleShutdown() : handleReboot()}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
		>
			{#if type === 'stop'}
				<div class="flex items-center">
					<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
					<span>Stop</span>
				</div>
			{:else if type === 'shutdown'}
				<div class="flex items-center">
					<span class="icon-[mdi--power] mr-1 h-4 w-4"></span>
					<span>Shutdown</span>
				</div>
			{:else if type === 'reboot'}
				<div class="flex items-center">
					<span class="icon-[mdi--restart] mr-1 h-4 w-4"></span>
					<span>Reboot</span>
				</div>
			{/if}
		</Button>
	{/if}
{/snippet}

<div>
	<div class="flex h-10 w-full items-center gap-1 border p-4">
		{#key data.vm.rid}
			<div class="flex items-center gap-1" transition:fade>
				{@render button('start')}
				{@render button('force-delete')}
				{@render button('force-stop')}
				{@render button('reboot')}
				{@render button('shutdown')}
				{@render button('stop')}

				{#if hasActiveLifecycleTask || isLifecycleTransitionPending}
					<Badge
						variant={lifecycleActionBadge.variant}
						class={`mt-0.5 ml-2 ${lifecycleActionBadge.className}`}
					>
						<span class="icon-[mdi--loading] mr-1 h-3 w-3 animate-spin"></span>
						{lifecycleActionBadge.label}
					</Badge>
				{/if}
			</div>
		{/key}

		<div class="ml-auto flex h-full items-center gap-2">
			{#if vmLogs.length > 0}
				<div transition:fade>
					<Button
						size="sm"
						onclick={() => {
							followLogs = true;
							showLogs = true;
							logs.refetch();
						}}
						class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
					>
						<div class="flex items-center">
							<span class="icon-[mdi--file-document-outline] h-4 w-4"></span>
							<span>View Logs</span>
						</div>
					</Button>
				</div>
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
				>{modalState.forceDelete ? 'Force Delete VM?' : 'Are you sure?'}</AlertDialogRaw.Title
			>
			<AlertDialogRaw.Description>
				{modalState.forceDelete ? `This will force delete VM` : `This will permanently delete VM`}
				<span class="font-semibold">{modalState?.title}.</span>
				{#if modalState.forceDelete}
					<div class="mt-2 text-sm">
						Best-effort cleanup will attempt libvirt/domain removal, VM datasets, VM DB records, and
						VM network objects. Partial failures will be tolerated.
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
				{/if}
			</AlertDialogRaw.Description>
		</AlertDialogRaw.Header>
		<AlertDialogRaw.Footer>
			<AlertDialogRaw.Cancel
				onclick={() => {
					modalState.isDeleteOpen = false;
					modalState.forceDelete = false;
				}}>Cancel</AlertDialogRaw.Cancel
			>
			<AlertDialogRaw.Action onclick={handleDelete}
				>{modalState.forceDelete ? 'Force Delete' : 'Continue'}</AlertDialogRaw.Action
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
