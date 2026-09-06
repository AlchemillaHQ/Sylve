<script lang="ts">
	import { getDownloadsResult } from '$lib/api/utilities/downloader';
	import { storageDelete, storageUpdate } from '$lib/api/vm/storage';
	import { getVmByIdResult, getVMsResult } from '$lib/api/vm/vm';
	import { getDatasetsResult } from '$lib/api/zfs/datasets';
	import { getPoolsResult } from '$lib/api/zfs/pool';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Storage from '$lib/components/custom/VM/Hardware/Storage.svelte';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { generateTableData } from '$lib/utils/vm/storage';
	import { toast } from 'svelte-sonner';
	import { resource, watch } from 'runed';
	import { getContext, onMount, untrack } from 'svelte';

	interface Data {
		vms: VM[];
		vm: VM;
		filesystems: Dataset[];
		volumes: Dataset[];
		pools: Zpool[];
		downloads: Download[];
		rid: number;
		node: string;
		loadErrors: APIResponse[];
	}

	interface DeleteTarget {
		id: number;
		name: string;
		kind: string;
		path: string;
		size: number;
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	const lastVMsByNode: Record<string, VM[]> = Object.create(null);
	lastVMsByNode[initialData.node] = initialData.vms;
	const vms = resource(
		() => data.node,
		async (node) => {
			const result = await getVMsResult({ hostname: node });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVMsByNode[node] ?? data.vms;
			}
			lastVMsByNode[node] = result;
			await updateCache('vm-list', result, node);
			return result;
		},
		{
			initialValue: initialData.vms
		}
	);

	const vmIdentity = (node: string, rid: number) => `${node}\u0000${rid}`;
	const lastVMByIdentity: Record<string, VM> = Object.create(null);
	lastVMByIdentity[vmIdentity(initialData.node, initialData.rid)] = initialData.vm;
	const vm = resource(
		() => [data.node, data.rid] as const,
		async ([node, rid]) => {
			const result = await getVmByIdResult(rid, { hostname: node });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVMByIdentity[vmIdentity(node, rid)] ?? data.vm;
			}
			lastVMByIdentity[vmIdentity(node, rid)] = result;
			await updateCache(`vm-${rid}`, result, node);
			return result;
		},
		{
			initialValue: initialData.vm
		}
	);

	const lastPoolsByNode: Record<string, Zpool[]> = Object.create(null);
	lastPoolsByNode[initialData.node] = initialData.pools;
	const pools = resource(
		() => data.node,
		async (node) => {
			const result = await getPoolsResult(false, { hostname: node });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastPoolsByNode[node] ?? data.pools;
			}
			lastPoolsByNode[node] = result;
			await updateCache('pool-list', result, node);
			return result;
		},
		{
			initialValue: initialData.pools
		}
	);

	const lastFilesystemsByNode: Record<string, Dataset[]> = Object.create(null);
	lastFilesystemsByNode[initialData.node] = initialData.filesystems;
	const filesystems = resource(
		() => data.node,
		async (node) => {
			const result = await getDatasetsResult(GZFSDatasetTypeSchema.enum.FILESYSTEM, node);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastFilesystemsByNode[node] ?? data.filesystems;
			}
			lastFilesystemsByNode[node] = result;
			await updateCache('zfs-filesystems', result, node);
			return result;
		},
		{
			initialValue: initialData.filesystems
		}
	);

	const lastVolumesByNode: Record<string, Dataset[]> = Object.create(null);
	lastVolumesByNode[initialData.node] = initialData.volumes;
	const volumes = resource(
		() => data.node,
		async (node) => {
			const result = await getDatasetsResult(GZFSDatasetTypeSchema.enum.VOLUME, node);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVolumesByNode[node] ?? data.volumes;
			}
			lastVolumesByNode[node] = result;
			await updateCache('zfs-volumes', result, node);
			return result;
		},
		{
			initialValue: initialData.volumes
		}
	);

	const lastDownloadsByNode: Record<string, Download[]> = Object.create(null);
	lastDownloadsByNode[initialData.node] = initialData.downloads;
	const downloads = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			const fallback = () =>
				lastDownloadsByNode[node] ?? (data.node === node ? data.downloads : []);
			try {
				const result = await getDownloadsResult({ hostname: node, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return fallback();
				}
				lastDownloadsByNode[node] = result;
				await updateCache('download-list', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return fallback();
				throw error;
			}
		},
		{
			initialValue: initialData.downloads
		}
	);

	function refreshData() {
		vm.refetch();
		vms.refetch();
		pools.refetch();
		filesystems.refetch();
		volumes.refetch();
	}

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let activeRows: Row[] = $state([]);
	let query: string = $state('');
	let datasets = $derived([...filesystems.current, ...volumes.current]);
	let tableData = $derived(generateTableData(vm.current, datasets, downloads.current));

	let properties = $state({
		attach: { open: false },
		edit: { open: false, id: null as number | null }
	});
	let reload = $state(false);
	let connectionLoading = $state(false);
	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<DeleteTarget | null>(null);
	let deleteBacking = $state(false);
	let deleteLoading = $state(false);
	let selectedRow = $derived(activeRows.length === 1 ? activeRows[0] : null);
	let canDeleteBacking = $derived(deleteTarget?.kind === 'raw' || deleteTarget?.kind === 'zvol');
	let operationLoading = $derived(connectionLoading || deleteLoading);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			refreshData();
			reload = false;
		}
	);

	function connectionLabel(kind: string, connected: boolean): string {
		if (kind === 'image') return connected ? 'Eject' : 'Insert';
		if (kind === 'filesystem') return connected ? 'Unshare' : 'Share';
		return connected ? 'Disconnect' : 'Connect';
	}

	function connectionIcon(kind: string, connected: boolean): string {
		if (kind === 'image') return connected ? 'icon-[mdi--eject]' : 'icon-[tdesign--cd-filled]';
		if (kind === 'filesystem') {
			return connected ? 'icon-[mdi--folder-remove-outline]' : 'icon-[mdi--folder-plus-outline]';
		}
		return connected ? 'icon-[mdi--lan-disconnect]' : 'icon-[mdi--lan-connect]';
	}

	function selectedActionTitle(action: string, readyLabel: string): string {
		if (!isDomainShutoff) return `VM must be shut off to ${action}`;
		return readyLabel;
	}

	function openDeleteDialog() {
		if (!selectedRow) return;

		const id = Number(selectedRow.id);
		if (!Number.isInteger(id) || id <= 0) return;

		deleteTarget = {
			id,
			name: String(selectedRow.name || 'Untitled storage'),
			kind: String(selectedRow.type || ''),
			path: String(selectedRow.path || ''),
			size: Number(selectedRow.size || 0)
		};
		deleteBacking = false;
		deleteDialogOpen = true;
	}

	async function toggleStorageConnection() {
		if (!selectedRow || !isDomainShutoff || operationLoading) return;

		const id = Number(selectedRow.id);
		if (!Number.isInteger(id) || id <= 0) return;

		const kind = String(selectedRow.type || '');
		const connect = !selectedRow.enabled;
		const label = connectionLabel(kind, !connect);
		connectionLoading = true;

		try {
			const response = await storageUpdate(
				data.rid,
				id,
				{ enable: connect },
				{ hostname: data.node }
			);
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error(`Failed to ${label.toLowerCase()} storage`, {
					position: 'bottom-center'
				});
				return;
			}

			activeRows = [];
			toast.success(`${label} complete`, {
				position: 'bottom-center'
			});
			reload = true;
		} catch (error) {
			toast.error(`Failed to ${label.toLowerCase()} storage`, {
				description: error instanceof Error ? error.message : undefined,
				position: 'bottom-center'
			});
		} finally {
			connectionLoading = false;
		}
	}

	async function deleteStorage(event: MouseEvent) {
		event.preventDefault();
		const target = deleteTarget;
		if (deleteLoading || !target) return;

		deleteLoading = true;
		try {
			const removeBacking = canDeleteBacking && deleteBacking;
			const response = await storageDelete(data.rid, target.id, {
				hostname: data.node,
				deleteBacking: removeBacking
			});
			if (response.status === 'error') {
				handleAPIError(response);
				reload = true;
				toast.error('Failed to delete storage', {
					position: 'bottom-center'
				});
				return;
			}

			deleteDialogOpen = false;
			activeRows = [];
			toast.success(
				removeBacking ? 'Storage and backing data deleted' : 'Storage attachment deleted',
				{ position: 'bottom-center' }
			);
			reload = true;
		} catch (error) {
			reload = true;
			toast.error('Failed to delete storage', {
				description: error instanceof Error ? error.message : undefined,
				position: 'bottom-center'
			});
		} finally {
			deleteLoading = false;
		}
	}

	let isLifecycleActive = $derived(!!domain.current?.pendingAction);
	let isDomainShutoff = $derived(
		!isLifecycleActive &&
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === 'shutoff'
	);
	let selectedActionDisabled = $derived(!isDomainShutoff || operationLoading);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => {
				properties.attach.open = true;
			}}
			size="sm"
			class="h-6"
			title={!isDomainShutoff ? 'VM must be shut off to add storage' : 'Add storage'}
			disabled={!isDomainShutoff || operationLoading}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="Add storage" />
		</Button>

		{#if selectedRow}
			<Button
				onclick={() => {
					if (!selectedRow) return;
					properties.edit.open = true;
					properties.edit.id = Number(selectedRow.id);
				}}
				size="sm"
				variant="outline"
				class="h-6"
				title={selectedActionTitle('edit storage', 'Edit storage')}
				disabled={selectedActionDisabled}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>

			<Button
				onclick={toggleStorageConnection}
				size="sm"
				variant="outline"
				class="h-6"
				title={selectedActionTitle(
					'change storage connections',
					connectionLabel(String(selectedRow?.type || ''), Boolean(selectedRow?.enabled))
				)}
				disabled={selectedActionDisabled}
			>
				{#if connectionLoading}
					<SpanWithIcon
						icon="icon-[mdi--loading]"
						size="h-4 w-4 animate-spin"
						gap="gap-1"
						title="Updating"
					/>
				{:else}
					<SpanWithIcon
						icon={connectionIcon(String(selectedRow?.type || ''), Boolean(selectedRow?.enabled))}
						size="h-4 w-4"
						gap="gap-1"
						title={connectionLabel(String(selectedRow?.type || ''), Boolean(selectedRow?.enabled))}
					/>
				{/if}
			</Button>

			<Button
				onclick={openDeleteDialog}
				size="sm"
				variant="outline"
				class="h-6"
				title={selectedActionTitle('delete storage', 'Delete storage')}
				disabled={selectedActionDisabled}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="tt-vm-storage"
		bind:parentActiveRow={activeRows}
		multipleSelect={true}
		initialSort={[{ column: 'bootorder', dir: 'asc' }]}
		bind:query
	/>
</div>

<AlertDialog.Root bind:open={deleteDialogOpen}>
	<AlertDialog.Content
		onInteractOutside={(event) => event.preventDefault()}
		onEscapeKeydown={(event) => {
			if (deleteLoading) event.preventDefault();
		}}
		aria-busy={deleteLoading}
		class="p-5"
	>
		<AlertDialog.Header>
			<AlertDialog.Title>Delete storage?</AlertDialog.Title>
			<AlertDialog.Description>
				<span class="font-medium text-foreground">“{deleteTarget?.name}”</span> will be removed from
				<span class="font-medium text-foreground">{vm.current.name}</span>.
				{#if deleteTarget?.kind === 'image'}
					The downloaded source will remain available in Downloader.
				{:else if deleteTarget?.kind === 'filesystem'}
					The shared ZFS dataset will remain on the host.
				{:else}
					Leave the option below off to keep the disk data on the host.
				{/if}
			</AlertDialog.Description>
		</AlertDialog.Header>

		<div class="rounded-md border bg-muted/30 px-3 py-2 text-sm">
			<div class="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3 gap-y-1">
				<span class="text-muted-foreground">Backing</span>
				<span class="min-w-0 break-all font-mono text-xs text-foreground">
					{deleteTarget?.path || 'Unavailable'}
				</span>
				{#if deleteTarget && deleteTarget.size > 0}
					<span class="text-muted-foreground">Size</span>
					<span class="text-foreground">{formatBytesBinary(deleteTarget.size)}</span>
				{/if}
			</div>
		</div>

		{#if canDeleteBacking}
			<div class="space-y-2 rounded-md border px-3 py-3">
				<CustomCheckbox
					label="Also permanently delete backing storage"
					bind:checked={deleteBacking}
					disabled={deleteLoading}
				/>
				{#if deleteBacking}
					<p class="text-sm text-destructive">
						The backing storage and all data in it will be permanently destroyed. This cannot be
						undone.
					</p>
				{/if}
			</div>
		{/if}

		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={deleteLoading}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action
				class="bg-destructive text-white hover:bg-destructive/90"
				onclick={deleteStorage}
				disabled={deleteLoading}
			>
				{#if deleteLoading}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Deleting
				{:else if canDeleteBacking && deleteBacking}
					Delete storage and backing
				{:else}
					Delete storage
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

{#if properties.attach.open}
	<Storage
		bind:open={properties.attach.open}
		node={data.node}
		storageId={null}
		{datasets}
		downloads={downloads.current}
		vm={vm.current}
		vms={vms.current}
		pools={pools.current}
		tableData={null}
		bind:reload
	/>
{/if}

{#if properties.edit.open}
	<Storage
		bind:open={properties.edit.open}
		node={data.node}
		storageId={properties.edit.id}
		{datasets}
		downloads={downloads.current}
		vm={vm.current}
		vms={vms.current}
		pools={pools.current}
		{tableData}
		bind:reload
	/>
{/if}
