<script lang="ts">
	import * as AlertDialogRaw from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { setContext } from 'svelte';
	import { page } from '$app/state';
	import {
		actionVm,
		deleteVM,
		forceDeleteVM,
		getSimpleVMByIdResult,
		getVMDomain,
		purgeVMRegistration
	} from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { storage } from '$lib';
	import { reload, vmPowerSignal } from '$lib/stores/api.svelte';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { SimpleVm, VMDomain } from '$lib/types/vm/vm';
	import { parseGuestDeletionData } from '$lib/types/common';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { removeStaleCacheByRID, getVMLifecycleBadgeStyle } from '$lib/utils/vm/vm';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let rid = $derived.by(() => {
		const value = Number(page.params.rid);
		return Number.isSafeInteger(value) && value > 0 ? value : 0;
	});
	let node = $derived(String(page.params.node || ''));
	let vmIdentity = $derived(`${node}\u0000${rid}`);

	type SimpleVMSnapshot = { identity: string; vm: SimpleVm };
	type VMDomainSnapshot = { identity: string; domain: VMDomain };
	const initialPageData = page.data as {
		node?: string;
		rid?: number;
		vm?: SimpleVm | null;
		domain?: VMDomain | null;
	};
	const initialIdentity = `${String(initialPageData.node || '')}\u0000${Number(initialPageData.rid || 0)}`;

	const vmResource = resource(
		[() => node, () => rid],
		async ([hostname, currentRid], _, { signal }): Promise<SimpleVMSnapshot | null> => {
			if (!currentRid || !hostname) return null;
			const result = await getSimpleVMByIdResult(currentRid, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load VM');
			}
			await updateCache(`simple-vm-${currentRid}`, result, hostname);
			return { identity: `${hostname}\u0000${currentRid}`, vm: result };
		},
		{
			initialValue: initialPageData.vm
				? { identity: initialIdentity, vm: initialPageData.vm }
				: null
		}
	);

	const domainResource = resource(
		[() => node, () => rid],
		async ([hostname, currentRid], _, { signal }): Promise<VMDomainSnapshot | null> => {
			if (!currentRid || !hostname) return null;
			const result = await getVMDomain(currentRid, { hostname, signal });
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load VM domain');
			}
			await updateCache(`vm-domain-${currentRid}`, result, hostname);
			return { identity: `${hostname}\u0000${currentRid}`, domain: result };
		},
		{
			initialValue: initialPageData.domain
				? { identity: initialIdentity, domain: initialPageData.domain }
				: null
		}
	);

	const vm = {
		get current(): SimpleVm | null {
			if (vmResource.error || vmResource.current?.identity !== vmIdentity) return null;
			return vmResource.current.vm;
		},
		refetch: () => vmResource.refetch()
	};

	const domain = {
		get current(): VMDomain | null {
			if (domainResource.error || domainResource.current?.identity !== vmIdentity) return null;
			return domainResource.current.domain;
		},
		refetch: () => domainResource.refetch()
	};

	setContext('vmDomain', domain);

	let normalizedDomainStatus = $derived.by(() =>
		String(domain.current?.status || '')
			.trim()
			.toLowerCase()
	);
	let isDomainErrorState = $derived(normalizedDomainStatus === 'error');
	let isOrphanState = $derived(normalizedDomainStatus === 'orphan');
	let hasActiveLifecycleTask = $derived(!!domain.current?.pendingAction);
	let lifecycleActionBadge = $derived(
		getVMLifecycleBadgeStyle(domain.current?.pendingAction || '')
	);
	let shouldHideActionButtons = $derived(hasActiveLifecycleTask);
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

	let vmChildRoute = $derived.by(() => {
		const segments = page.url.pathname.split('/').filter(Boolean);
		const vmIndex = segments.indexOf('vm');
		if (vmIndex === -1) return '';
		return segments[vmIndex + 2] ?? '';
	});
	let isSummaryPage = $derived.by(() => vmChildRoute === '' || vmChildRoute === 'summary');
	let isConsolePage = $derived.by(() => vmChildRoute === 'console');

	const visible = new IsDocumentVisible();

	let isDeleteInFlight = $state(false);

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

	async function refreshVmDomain() {
		if (!rid || isDeleteInFlight) return;
		await Promise.all([vm.refetch(), domain.refetch()]);
	}

	watch(
		() => rid,
		(newRid) => {
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
		modalState.purgeOnly = false;
		modalState.forceDelete = forceDelete;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = forceDelete;
		modalState.deleteVolumes = forceDelete;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	function openRemoveModal() {
		if (!vm.current) return;
		modalState.purgeOnly = true;
		modalState.forceDelete = false;
		modalState.deleteMACs = true;
		modalState.deleteRAWDisks = false;
		modalState.deleteVolumes = false;
		modalState.title = `${vm.current.name} (${vm.current.rid})`;
		modalState.isDeleteOpen = true;
	}

	async function handleDelete() {
		if (!vm.current || isDeleteInFlight) return;
		const target = {
			rid: vm.current.rid,
			name: vm.current.name,
			hostname: node
		};
		const wasForceDelete = modalState.forceDelete;
		const wasPurgeOnly = modalState.purgeOnly;

		isDeleteInFlight = true;
		modalState.isDeleteOpen = false;
		modalState.loading.open = true;
		modalState.loading.title = wasPurgeOnly
			? 'Removing Stale VM Entry'
			: wasForceDelete
				? 'Force Deleting Virtual Machine'
				: 'Deleting Virtual Machine';
		modalState.loading.description = wasPurgeOnly
			? `Removing stale registration for VM <b>${target.name} (${target.rid})</b>; datasets are preserved`
			: wasForceDelete
				? `Please wait while VM <b>${target.name} (${target.rid})</b> is being force deleted with best-effort cleanup`
				: `Please wait while VM <b>${target.name} (${target.rid})</b> is being deleted`;

		try {
			const result = wasPurgeOnly
				? await purgeVMRegistration(target.rid, modalState.deleteMACs, target.hostname)
				: wasForceDelete
					? await forceDeleteVM(target.rid, modalState.deleteMACs, target.hostname)
					: await deleteVM(
							target.rid,
							modalState.deleteMACs,
							modalState.deleteRAWDisks,
							modalState.deleteVolumes,
							target.hostname
						);

			if (result.status === 'error') {
				await Promise.all([vm.refetch(), domain.refetch()]);
				toast.error(
					result.message === 'guest_delete_requires_backup_jobs_removed'
						? 'Remove all backup jobs before deleting this VM'
						: result.message === 'guest_delete_requires_replication_policy_removed'
							? 'Remove the replication policy before deleting this VM'
							: wasPurgeOnly
								? 'Error removing VM entry'
								: wasForceDelete
									? 'Error force deleting VM'
									: 'Error deleting VM',
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
			await removeStaleCacheByRID(target.rid, target.hostname);
			await useSafeGoto(`/${target.hostname}/summary`);

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
		} finally {
			modalState.loading.open = false;
			modalState.forceDelete = false;
			modalState.purgeOnly = false;
			isDeleteInFlight = false;
		}
	}

	async function handleStart() {
		if (!vm.current) return;
		const result = await actionVm(vm.current.rid, 'start', node);

		if (isAPIResponse(result)) {
			toast.error(
				result.message === 'lifecycle_task_in_progress' ||
					result.message === 'migration_in_progress'
					? 'VM action already in progress'
					: 'Error starting VM',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			reload.leftPanel = true;
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
		const result = await actionVm(vm.current.rid, 'stop', node);

		if (isAPIResponse(result)) {
			toast.error(
				result.message === 'lifecycle_task_in_progress' ||
					result.message === 'migration_in_progress'
					? 'VM action already in progress'
					: 'Error stopping VM',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			reload.leftPanel = true;
			vmPowerSignal.token += 1;
			vmPowerSignal.rid = vm.current.rid;
			vmPowerSignal.action = 'stop';

			if (result.outcome === 'force_stop_requested') {
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
		const result = await actionVm(vm.current.rid, 'stop', node);

		if (isAPIResponse(result)) {
			toast.error(
				result.message === 'lifecycle_task_in_progress' ||
					result.message === 'migration_in_progress'
					? 'VM action already in progress'
					: 'Error requesting force stop',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			reload.leftPanel = true;
			toast.warning('Force stop requested', {
				duration: 5000,
				position: 'bottom-center'
			});
		}

		await refreshVmDomain();
	}

	async function handleShutdown() {
		if (!vm.current) return;
		const result = await actionVm(vm.current.rid, 'shutdown', node);

		if (isAPIResponse(result)) {
			toast.error(
				result.message === 'lifecycle_task_in_progress' ||
					result.message === 'migration_in_progress'
					? 'VM action already in progress'
					: 'Error shutting down VM',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			reload.leftPanel = true;
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
		const result = await actionVm(vm.current.rid, 'reboot', node);

		if (isAPIResponse(result)) {
			toast.error(
				result.message === 'lifecycle_task_in_progress' ||
					result.message === 'migration_in_progress'
					? 'VM action already in progress'
					: 'Error rebooting VM',
				{
					duration: 5000,
					position: 'bottom-center'
				}
			);
		} else {
			reload.leftPanel = true;
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
					{#if hasActiveLifecycleTask}
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

			{#key `${node}:${rid}`}
				<div class="flex items-center gap-1">
					{#if vm.current && domain.current}
						{#if !shouldHideActionButtons && domain.current.id === -1 && !isDomainRunningForActions && !isDomainErrorState && !isOrphanState}
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
						{/if}

						{#if !shouldHideActionButtons && isDomainErrorState}
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
						{/if}

						{#if (domain.current.id !== -1 || domain.current?.pendingAction === 'start' || domain.current?.pendingAction === 'reboot') && isDomainRunningForActions}
							{#if isShutdownTaskActive}
								<Button
									onclick={() => handleForceStop()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-red-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<SpanWithIcon
										icon="icon-[mdi--alert]"
										size="h-4 w-4"
										gap="gap-1"
										title="Force Stop"
									/>
								</Button>
							{/if}

							{#if !shouldHideActionButtons}
								<Button
									onclick={() => handleReboot()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<SpanWithIcon
										icon="icon-[mdi--restart]"
										size="h-4 w-4"
										gap="gap-1"
										title="Reboot"
									/>
								</Button>

								<Button
									onclick={() => handleShutdown()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<SpanWithIcon
										icon="icon-[mdi--power]"
										size="h-4 w-4"
										gap="gap-1"
										title="Shutdown"
									/>
								</Button>

								<Button
									onclick={() => handleStop()}
									size="sm"
									class="bg-muted-foreground/40 dark:bg-muted disabled:pointer-events-auto! h-6 text-black hover:bg-yellow-600 disabled:hover:bg-neutral-600 dark:text-white"
								>
									<SpanWithIcon icon="icon-[mdi--stop]" size="h-4 w-4" gap="gap-1" title="Stop" />
								</Button>
							{/if}
						{/if}
					{/if}

					{#if isOrphanState && !shouldHideActionButtons}
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
		{#key `${node}:${rid}`}
			{@render children?.()}
		{/key}
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
