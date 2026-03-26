<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import { getActiveLifecycleTaskForGuest } from '$lib/api/task/lifecycle';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { goto } from '$app/navigation';
	import { setContext } from 'svelte';
	import { page } from '$app/state';
	import { actionVm, deleteVM, getSimpleVMById, getVMDomain } from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import { reload, vmPowerSignal } from '$lib/stores/api.svelte';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import { sleep } from '$lib/utils';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { fade } from 'svelte/transition';
	import type { SimpleVm, VMDomain, VMLifecycleAction } from '$lib/types/vm/vm';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import {
		createVMPendingLifecycleSnapshot,
		getEffectiveVMLifecycleAction,
		getVMLifecyclePendingTimeoutMs,
		getVMLifecycleBadgeStyle,
		isVMPendingLifecycleActionSettled,
		isVMLifecycleTransitionPending,
		markVMPendingSnapshotNonRunning,
		shouldHideVMLifecycleButtons,
		removeStaleCacheByRID,
		type VMPendingLifecycleSnapshot
	} from '$lib/utils/vm/vm';
	import type { APIResponse } from '$lib/types/common';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();
	let pendingLifecycleAction = $state<VMLifecycleAction | ''>('');
	let pendingLifecycleSnapshot = $state<VMPendingLifecycleSnapshot | null>(null);
	let pendingLifecycleTimer: ReturnType<typeof setTimeout> | null = null;
	const NON_RUNNING_ACTION_SUPPRESS_MS = 1800;
	let suppressNonRunningActions = $state(false);
	let suppressNonRunningActionsTimer: ReturnType<typeof setTimeout> | null = null;
	let isDeleteInFlight = $state(false);

	let rid = $derived.by(() => {
		const value = Number(page.url.pathname.split('/')[3]);
		return Number.isFinite(value) ? value : 0;
	});

	const vm = resource(
		() => `simple-vm-${rid}`,
		async (key: string): Promise<SimpleVm | null> => {
			if (!rid) return null;
			const result = await getSimpleVMById(rid, 'rid');
			if (isAPIResponse(result)) {
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: null as SimpleVm | null }
	);

	const domain = resource(
		() => `vm-domain-${rid}`,
		async (key: string): Promise<VMDomain | null> => {
			if (!rid) return null;
			const result = await getVMDomain(rid);
			if (isAPIResponse(result)) {
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: (page.data as { domain?: VMDomain | null }).domain ?? null }
	);

	const lifecycleTask = resource(
		() => `vm-lifecycle-task-${rid}`,
		async (): Promise<LifecycleTask | null | APIResponse> => {
			if (!rid) return null;
			const result = await getActiveLifecycleTaskForGuest('vm', rid);
			if (isAPIResponse(result)) {
				return null;
			}
			return result;
		},
		{ initialValue: null as LifecycleTask | null }
	);

	setContext('vmDomain', domain);
	setContext('vmLifecycleTask', lifecycleTask);

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);

	let isDomainErrorState = $derived.by(() => normalizedDomainStatus === 'error');
	let hasLifecycleTaskRecord = $derived(!!lifecycleTask.current);
	let activeLifecycleAction = $derived.by(() => {
		if (lifecycleTask.current && !isAPIResponse(lifecycleTask.current)) {
			return lifecycleTask.current.action;
		}

		return '';
	});
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
				: createVMPendingLifecycleSnapshot(String(domain.current?.status || ''));

		return isVMPendingLifecycleActionSettled(
			activeLifecycleAction,
			snapshot,
			normalizedDomainStatus,
			isDomainErrorState
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
	let isShutdownTaskActive = $derived.by(() => {
		if (!lifecycleTask.current || isAPIResponse(lifecycleTask.current)) return false;
		return lifecycleTask.current.action === 'shutdown' && !lifecycleTask.current.overrideRequested;
	});

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
		() => [pendingLifecycleAction, hasLifecycleTaskRecord, normalizedDomainStatus] as const,
		([pendingAction, hasTask]) => {
			if (!pendingAction || hasTask) {
				return;
			}

			if (
				isVMPendingLifecycleActionSettled(
					pendingAction,
					pendingLifecycleSnapshot,
					normalizedDomainStatus,
					isDomainErrorState
				)
			) {
				clearPendingLifecycleAction();
			}
		}
	);

	let vmChildRoute = $derived.by(() => {
		const segments = page.url.pathname.split('/').filter(Boolean);
		const vmIndex = segments.indexOf('vm');
		if (vmIndex === -1) return '';
		return segments[vmIndex + 2] ?? '';
	});
	let isSummaryPage = $derived.by(() => vmChildRoute === '' || vmChildRoute === 'summary');
	let isConsolePage = $derived.by(() => vmChildRoute === 'console');

	const visible = new IsDocumentVisible();

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

	async function refreshVmDomain() {
		if (!rid || isDeleteInFlight) return;
		await Promise.all([vm.refetch(), domain.refetch(), lifecycleTask.refetch()]);
	}

	watch(
		() => rid,
		(newRid) => {
			clearNonRunningActionsSuppression();
			if (newRid) {
				refreshVmDomain();
			}
		}
	);

	useInterval(() => 10000, {
		callback: () => {
			if (visible.current && rid && !isDeleteInFlight) {
				domain.refetch();
			}
		}
	});

	useInterval(() => 1500, {
		callback: () => {
			if (visible.current && rid && !isDeleteInFlight) {
				lifecycleTask.refetch();
			}
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle && rid && !isDeleteInFlight) {
				refreshVmDomain();
			}
		}
	);

	function openDeleteModal(forceDelete: boolean = false) {
		if (!vm.current) return;
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
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
			String(domain.current?.status || '')
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

	async function handleDelete() {
		if (!vm.current) return;
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
			await refreshVmDomain();
			toast.error(wasForceDelete ? 'Error force deleting VM' : 'Error deleting VM', {
				duration: 5000,
				position: 'bottom-center'
			});
		} else if (result.status === 'success') {
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			await goto(`/${storage.hostname}/summary`);
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

	async function handleStart() {
		if (!vm.current) return;
		beginPendingLifecycleAction('start');
		const result = await actionVm(vm.current.rid, 'start');
		reload.leftPanel = true;

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
		} else if (result.status === 'success') {
			vmPowerSignal.token += 1;
			vmPowerSignal.rid = vm.current.rid;
			vmPowerSignal.action = 'start';

			toast.success('VM start queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleStop() {
		if (!vm.current) return;
		beginPendingLifecycleAction('stop');
		const result = await actionVm(vm.current.rid, 'stop');
		reload.leftPanel = true;

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
			vmPowerSignal.token += 1;
			vmPowerSignal.rid = vm.current.rid;
			vmPowerSignal.action = 'stop';

			if (result.message === 'vm_force_stop_requested') {
				toast.warning('Force stop requested', {
					duration: 5000,
					position: 'bottom-center'
				});
			} else {
				toast.success('VM stop queued', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
		}

		await refreshVmDomain();
	}

	async function handleForceStop() {
		if (!vm.current) return;
		beginPendingLifecycleAction('stop');
		const result = await actionVm(vm.current.rid, 'stop');
		reload.leftPanel = true;

		if (result.status === 'error') {
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

		await refreshVmDomain();
	}

	async function handleShutdown() {
		if (!vm.current) return;
		beginPendingLifecycleAction('shutdown');
		const result = await actionVm(vm.current.rid, 'shutdown');
		reload.leftPanel = true;

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
			vmPowerSignal.token += 1;
			vmPowerSignal.rid = vm.current.rid;
			vmPowerSignal.action = 'shutdown';

			toast.success('VM shutdown queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleReboot() {
		if (!vm.current) return;
		beginPendingLifecycleAction('reboot');
		const result = await actionVm(vm.current.rid, 'reboot');
		reload.leftPanel = true;

		if (result.status === 'error') {
			clearPendingLifecycleAction();
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
			vmPowerSignal.token += 1;
			vmPowerSignal.rid = vm.current.rid;
			vmPowerSignal.action = 'reboot';

			toast.success('VM reboot queued', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}
</script>

<div class="flex h-full min-h-0 w-full flex-col">
	{#if !isSummaryPage}
		<div class="flex h-10 w-full shrink-0 items-center justify-between gap-1 border p-4">
			<div class="min-w-0 flex items-center gap-2">
				{#if vm.current && domain.current}
					<Badge
						variant="outline"
						class="text-muted-foreground px-1.5"
						title={domain.current.status}
					>
						{#if normalizedDomainStatus === 'running'}
							<span class="icon-[mdi--check-circle] text-green-500"></span>
						{:else if isDomainErrorState}
							<span class="icon-[mdi--alert-circle] text-red-500"></span>
						{:else}
							<span class="icon-[mdi--close-circle] text-gray-500"></span>
						{/if}
					</Badge>
					<p class="truncate text-sm font-semibold">{vm.current.name} ({vm.current.rid})</p>
					{#if hasActiveLifecycleTask || isLifecycleTransitionPending}
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

			{#key rid}
				<div class="flex items-center gap-1" in:fade={{ delay: 140, duration: 220 }}>
					{#if vm.current && domain.current}
						{#if !shouldHideActionButtons && domain.current.id === -1 && !isDomainRunningForActions && !isDomainErrorState}
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
						{/if}

						{#if !shouldHideActionButtons && isDomainErrorState}
							<Button
								onclick={() => openDeleteModal(true)}
								size="sm"
								class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! ml-2 h-6 text-black hover:bg-red-700 disabled:hover:bg-neutral-600 dark:text-white"
							>
								<span class="icon-[mdi--alert-octagon] mr-1 h-4 w-4"></span>
								<span>Force Delete</span>
							</Button>
						{/if}

						{#if (domain.current.id !== -1 || suppressNonRunningActions) && isDomainRunningForActions}
							{#if isShutdownTaskActive}
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
							{/if}

							{#if !shouldHideActionButtons}
								<Button
									onclick={() => handleReboot()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<div class="flex items-center">
										<span class="icon-[mdi--restart] mr-1 h-4 w-4"></span>
										<span>Reboot</span>
									</div>
								</Button>

								<Button
									onclick={() => handleShutdown()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<div class="flex items-center">
										<span class="icon-[mdi--power] mr-1 h-4 w-4"></span>
										<span>Shutdown</span>
									</div>
								</Button>

								<Button
									onclick={() => handleStop()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<div class="flex items-center">
										<span class="icon-[mdi--stop] mr-1 h-4 w-4"></span>
										<span>Stop</span>
									</div>
								</Button>
							{/if}
						{/if}
					{/if}
				</div>
			{/key}
		</div>
	{/if}

	<div
		class="min-h-0 flex-1"
		class:overflow-hidden={isConsolePage}
		class:overflow-auto={!isConsolePage}
	>
		{@render children?.()}
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
