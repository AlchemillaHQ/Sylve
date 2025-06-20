<script lang="ts">
	import { page } from '$app/state';
	import AlertDialog from '$lib/components/custom/AlertDialog.svelte';

	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card/index.js';

	import { actionVm, deleteVM, getVMDomain, getVMs } from '$lib/api/vm/vm';
	import LoadingDialog from '$lib/components/custom/LoadingDialog.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';

	import { hostname } from '$lib/stores/basic';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { sleep } from '$lib/utils';
	import { updateCache } from '$lib/utils/http';

	import { getDownloads } from '$lib/api/utilities/downloader';
	import { storageDetach } from '$lib/api/vm/storage';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { getTranslation } from '$lib/utils/i18n';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { capitalizeFirstLetter } from '$lib/utils/string';
	import { dateToAgo } from '$lib/utils/time';
	import { generateTableData } from '$lib/utils/vm/storage';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import { t } from 'svelte-i18n';
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

	function handleCreate() {
		toast.error('Not implemented yet', {
			position: 'bottom-center'
		});
	}

	async function handleDetach() {
		await storageDetach(Number(vmId), Number(activeRows[0].id));
	}
</script>

{#snippet button(type: string)}
	{#if domain && domain.status === 'Shutoff' && activeRows && activeRows.length === 1}
		{#if type === 'detach'}
			<Button onclick={() => handleDetach()} size="sm" class="h-6">
				<Icon icon="gg:remove" class="mr-1 h-4 w-4" />
				{capitalizeFirstLetter(getTranslation('common.detach', 'Detach'))}
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button onclick={() => handleCreate()} size="sm" class="h-6  ">
			<Icon icon="gg:add" class="mr-1 h-4 w-4" />
			{capitalizeFirstLetter(getTranslation('common.new', 'New'))}
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
