<script lang="ts">
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { storageDetach } from '$lib/api/vm/storage';
	import { getVMDomain, getVMs } from '$lib/api/vm/vm';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Storage from '$lib/components/custom/VM/Hardware/Storage.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { useQueries } from '$lib/runes/useQuery.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/vm/storage';
	import { toast } from 'svelte-sonner';

	interface Data {
		vms: VM[];
		domain: VMDomain;
		datasets: Dataset[];
		pools: Zpool[];
		downloads: Download[];
		vmId: string;
	}

	let { data }: { data: Data } = $props();

	const {
		vms: vmsQuery,
		domain: domainQuery,
		pools: poolsQuery,
		datasets: datasetsQuery,
		downloads: downloadsQuery,
		refetchAll
	} = useQueries(() => ({
		vms: () => ({
			key: 'vm-list',
			queryFn: () => getVMs(),
			initialData: data.vms,
			onSuccess: (f: VM[]) => {
				updateCache('vm-list', f);
			},
			refetchInterval: 1000
		}),
		domain: () => ({
			key: `vm-domain-${data.vmId}`,
			queryFn: () => getVMDomain(Number(data.vmId)),
			initialData: data.domain,
			onSuccess: (f: VMDomain) => {
				updateCache(`vm-domain-${data.vmId}`, f);
			},
			refetchInterval: 1000
		}),
		pools: () => ({
			key: 'pool-list',
			queryFn: () => getPools(),
			initialData: data.pools,
			onSuccess: (f: Zpool[]) => {
				updateCache('pool-list', f);
			},
			refetchInterval: 1000
		}),
		datasets: () => ({
			key: 'dataset-list',
			queryFn: () => getDatasets(),
			initialData: data.datasets,
			onSuccess: (f: Dataset[]) => {
				updateCache('datasets', f);
			},
			refetchInterval: 1000
		}),
		downloads: () => ({
			key: 'download-list',
			queryFn: () => getDownloads(),
			initialData: data.downloads,
			onSuccess: (f: Download[]) => {
				updateCache('download-list', f);
			},
			refetchInterval: 1000
		})
	}));

	let activeRows: Row[] = $state([]);
	let query: string = $state('');
	let vms: VM[] = $derived(vmsQuery.data);
	let pools: Zpool[] = $derived(poolsQuery.data);
	let datasets: Dataset[] = $derived(datasetsQuery.data);
	let downloads: Download[] = $derived(downloadsQuery.data);
	let vm: VM = $derived(
		vmsQuery.data.find((vm: VM) => vm.vmId === parseInt(data.vmId)) || ({} as VM)
	);
	let domain: VMDomain = $derived(domainQuery.data);

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
					<span class="icon-[gg--remove] mr-1 h-4 w-4"></span>

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
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

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
			let response = await storageDetach(Number(data.vmId), properties.detach.id as number);
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
			}

			properties.detach.open = false;
		},
		onCancel: () => {
			properties = options;
			properties.detach.open = false;
		}
	}}
/>

<Storage bind:open={properties.attach.open} {datasets} {downloads} {vm} {vms} {pools} />
