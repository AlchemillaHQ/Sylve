<script lang="ts">
	import { page } from '$app/state';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { storageDetach } from '$lib/api/vm/storage';
	import { getVMDomain, getVMs } from '$lib/api/vm/vm';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Storage from '$lib/components/custom/VM/Hardware/Storage.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/vm/storage';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		vms: VM[];
		domain: VMDomain;
		datasets: Dataset[];
		pools: Zpool[];
		downloads: Download[];
	}

	let { data }: { data: Data } = $props();
	const vmId = page.url.pathname.split('/')[3];

	const results = useQueries([
		{
			queryKey: ['vm-list'],
			queryFn: async () => {
				return await getVMs();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.vms,
			onSuccess: (data: VM[]) => {
				updateCache('vm-list', data);
			}
		},
		{
			queryKey: [`vm-domain-${vmId}`],
			queryFn: async () => {
				return await getVMDomain(vmId);
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.domain,
			onSuccess: (data: VMDomain) => {
				updateCache(`vm-domain-${vmId}`, data);
			}
		},
		{
			queryKey: ['poolList'],
			queryFn: async () => {
				return await getPools();
			},
			refetchInterval: 1000,
			keepPreviousData: false,
			initialData: data.pools,
			onSuccess: (data: Zpool[]) => {
				updateCache('pools', data);
			}
		},
		{
			queryKey: ['datasetList'],
			queryFn: async () => {
				return await getDatasets();
			},
			refetchInterval: 1000,
			keepPreviousData: false,
			initialData: data.datasets,
			onSuccess: (data: Dataset[]) => {
				updateCache('datasets', data);
			}
		},
		{
			queryKey: ['downloads'],
			queryFn: async () => {
				return await getDownloads();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.downloads,
			onSuccess: (data: Download[]) => {
				updateCache('downloads', data);
			}
		}
	]);

	let activeRows: Row[] = $state([]);
	let query: string = $state('');
	let domain: VMDomain = $derived($results[1].data as VMDomain);
	let vm: VM = $derived(
		($results[0].data as VM[]).find((vm: VM) => vm.vmId === parseInt(vmId)) || ({} as VM)
	);
	let datasets: Dataset[] = $derived($results[3].data as Dataset[]);
	let downloads: Download[] = $derived($results[4].data as Download[]);
	let tableData = $derived(generateTableData(vm, datasets, downloads));

	let options = {
		attach: {
			open: false
		},
		detach: {
			open: false,
			id: null as number | null,
			name: ''
		},
		setBootOrder: {
			open: false
		}
	};

	let properties = $state(options);
</script>

{#snippet button(type: string)}
	{#if domain && domain.status === 'Shutoff'}
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
					<Icon icon="gg:remove" class="mr-1 h-4 w-4" />
					<span>Detach</span>
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
			title={domain && domain.status !== 'Shutoff' ? 'VM must be shut off to attach storage' : ''}
			disabled={domain && domain.status !== 'Shutoff'}
		>
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New</span>
			</div>
		</Button>

		{@render button('detach')}
	</div>

	<TreeTable
		data={tableData}
		name={'tt-vm-storage'}
		bind:parentActiveRow={activeRows}
		multipleSelect={true}
		bind:query
	/>
</div>

<AlertDialog
	open={properties.detach.open}
	customTitle={`This will detach the storage ${properties.detach.name} from the VM <b>${vm.name}</b>`}
	actions={{
		onConfirm: async () => {
			let response = await storageDetach(Number(vmId), properties.detach.id as number);
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to detach storage', {
					position: 'bottom-center'
				});
			} else {
				toast.success('Storage detached', {
					position: 'bottom-center'
				});
			}

			properties.detach.open = false;
		},
		onCancel: () => {
			properties = options;
			properties.detach.open = false;
		}
	}}
/>

<Storage bind:open={properties.attach.open} {datasets} {downloads} {vm} />
