<script lang="ts">
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { storageDetach } from '$lib/api/vm/storage';
	import { getVmById, getVMs } from '$lib/api/vm/vm';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Storage from '$lib/components/custom/VM/Hardware/Storage.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/vm/storage';
	import { toast } from 'svelte-sonner';
	import { resource, watch } from 'runed';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import { getContext } from 'svelte';

	interface Data {
		vms: VM[];
		vm: VM;
		filesystems: Dataset[];
		volumes: Dataset[];
		pools: Zpool[];
		downloads: Download[];
		rid: string;
	}

	let { data }: { data: Data } = $props();

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');
	const lifecycleTask = getContext<{ current: LifecycleTask | null; refetch(): void }>(
		'vmLifecycleTask'
	);

	// svelte-ignore state_referenced_locally
	const vms = resource(
		() => 'vm-list',
		async (key) => {
			const result = await getVMs();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.vms
		}
	);

	// svelte-ignore state_referenced_locally
	const vm = resource(
		() => `vm-${data.rid}`,
		async (key) => {
			const result = await getVmById(Number(data.rid), 'rid');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.vm
		}
	);

	// svelte-ignore state_referenced_locally
	const pools = resource(
		() => 'pool-list',
		async (key) => {
			const result = await getPools();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.pools
		}
	);

	// svelte-ignore state_referenced_locally
	const datasets = resource(
		() => 'zfs-filesystems',
		async (key) => {
			const results = await Promise.all([
				getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM),
				getDatasets(GZFSDatasetTypeSchema.enum.VOLUME)
			]);

			const result = [...results[0], ...results[1]];
			updateCache(key, result);

			return result;
		},
		{
			initialValue: [...data.filesystems, ...data.volumes]
		}
	);

	// svelte-ignore state_referenced_locally
	const downloads = resource(
		() => 'download-list',
		async (key) => {
			const result = await getDownloads();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.downloads
		}
	);

	function refreshData() {
		vm.refetch();
		vms.refetch();
		pools.refetch();
		datasets.refetch();
		downloads.refetch();
	}

	let activeRows: Row[] = $state([]);
	let query: string = $state('');
	let tableData = $derived(generateTableData(vm.current, datasets.current, downloads.current));

	let options = {
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

	let properties = $state(options);
	let reload = $state(false);

	watch(
		() => reload,
		() => {
			refreshData();
			reload = false;
		}
	);

	let isLifecycleActive = $derived(
		!!lifecycleTask.current && !!(lifecycleTask.current as LifecycleTask).action
	);
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
				<div class="flex items-center">
					<span class="icon-[gg--remove] mr-1 h-4 w-4"></span>
					<span>Detach</span>
				</div>
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
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
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
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('detach')}
	</div>

	<TreeTable
		data={tableData}
		name="tt-vm-storage"
		bind:parentActiveRow={activeRows}
		multipleSelect={true}
		bind:query
	/>
</div>

<AlertDialog
	open={properties.detach.open}
	customTitle={`This will detach the storage ${properties.detach.name} from the VM <b>${vm.current.name}</b>. The underlying disk dataset/file will NOT be deleted.`}
	actions={{
		onConfirm: async () => {
			let response = await storageDetach(Number(data.rid), properties.detach.id as number);
			reload = true;
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
			properties = options;
			properties.detach.open = false;
		}
	}}
/>

{#if properties.attach.open}
	<Storage
		bind:open={properties.attach.open}
		storageId={null}
		datasets={datasets.current}
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
		storageId={properties.edit.id}
		datasets={datasets.current}
		downloads={downloads.current}
		vm={vm.current}
		vms={vms.current}
		pools={pools.current}
		{tableData}
		bind:reload
	/>
{/if}
