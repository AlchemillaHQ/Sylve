<script lang="ts">
	import { getDownloadsResult } from '$lib/api/utilities/downloader';
	import { storageDetach } from '$lib/api/vm/storage';
	import { getVmByIdResult, getVMsResult } from '$lib/api/vm/vm';
	import { getDatasetsResult } from '$lib/api/zfs/datasets';
	import { getPoolsResult } from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Storage from '$lib/components/custom/VM/Hardware/Storage.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
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
	import { escapeHTML } from '$lib/utils/string';
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

	function createPageOptions() {
		return {
			attach: {
				open: false
			},
			detach: {
				open: false,
				id: null as number | null,
				name: ''
			},
			edit: {
				open: false,
				id: null as number | null
			}
		};
	}

	let properties = $state(createPageOptions());
	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			refreshData();
			reload = false;
		}
	);

	let isLifecycleActive = $derived(!!domain.current?.pendingAction);
	let isDomainShutoff = $derived(
		!isLifecycleActive &&
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === 'shutoff'
	);
</script>

{#snippet button(type: string)}
	{#if isDomainShutoff}
		{#if type === 'detach' && activeRows && activeRows.length === 1}
			<Button
				onclick={() => {
					properties.detach.open = true;
					properties.detach.id = activeRows[0].id as number;
					properties.detach.name = activeRows[0].name as string;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[gg--remove]" size="h-4 w-4" gap="gap-1" title="Detach" />
			</Button>
		{/if}

		{#if type === 'edit' && activeRows && activeRows.length === 1}
			<Button
				onclick={() => {
					properties.edit.open = true;
					properties.edit.id = activeRows[0].id as number;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => {
				properties.attach.open = true;
			}}
			size="sm"
			class="h-6"
			title={!isDomainShutoff ? 'VM must be shut off to attach storage' : ''}
			disabled={!isDomainShutoff}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
		</Button>

		{@render button('edit')}
		{@render button('detach')}
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

<AlertDialog
	open={properties.detach.open}
	customTitle={`This will detach the storage ${escapeHTML(properties.detach.name)} from the VM <b>${escapeHTML(vm.current.name)}</b>. The underlying disk dataset/file will NOT be deleted.`}
	actions={{
		onConfirm: async () => {
			const response = await storageDetach(data.rid, properties.detach.id as number, {
				hostname: data.node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to detach storage', {
					position: 'bottom-center'
				});
			} else {
				activeRows = [];
				toast.success('Storage detached', {
					position: 'bottom-center'
				});
				reload = true;
			}

			properties.detach.open = false;
		},
		onCancel: () => {
			properties = createPageOptions();
		}
	}}
/>

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
