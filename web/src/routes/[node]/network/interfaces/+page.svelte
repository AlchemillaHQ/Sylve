<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import KvTableModal from '$lib/components/custom/KVTableModal.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { type Iface } from '$lib/types/network/iface';
	import { updateCache } from '$lib/utils/http';
	import { generateTableData, getCleanIfaceData } from '$lib/utils/network/iface';
	import { renderWithIcon } from '$lib/utils/table';
	import { createQueries } from '@tanstack/svelte-query';
	import type { CellComponent } from 'tabulator-tables';
	import { getNetworkObjects } from '$lib/api/network/object';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { Jail } from '$lib/types/jail/jail';
	import { getJails } from '$lib/api/jail/jail';
	import { isMACNearOrEqual } from '$lib/utils/mac';
	import type { VM } from '$lib/types/vm/vm';
	import { getVMs } from '$lib/api/vm/vm';

	interface Data {
		interfaces: Iface[];
		objects: NetworkObject[];
		jails: Jail[];
		vms: VM[];
	}

	let { data }: { data: Data } = $props();

	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['networkInterfaces'],
				queryFn: async () => {
					return await getInterfaces();
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: data.interfaces,
				onSuccess: (data: Iface[]) => {
					updateCache('networkInterfaces', data);
				}
			},
			{
				queryKey: ['networkObjects'],
				queryFn: async () => {
					return await getNetworkObjects();
				},
				keepPreviousData: true,
				initialData: data.objects,
				onSuccess: (data: NetworkObject[]) => {
					updateCache('networkObjects', data);
				}
			},
			{
				queryKey: ['jail-list'],
				queryFn: async () => {
					return await getJails();
				},
				initialData: data.jails,
				keepPreviousData: true,
				refetchOnMount: 'always'
			},
			{
				queryKey: ['vm-list'],
				queryFn: async () => {
					return await getVMs();
				},
				refetchInterval: 1000,
				initialData: data.vms,
				keepPreviousData: true,
				refetchOnMount: 'always',
				onSuccess: (data: VM[]) => {
					updateCache('vm-list', data);
				}
			}
		]
	}));

	let jails = $derived(results[2].data as Jail[]);
	let vms = $derived(results[3].data as VM[]);

	let columns: Column[] = $derived([
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter(cell: CellComponent) {
				const value = cell.getValue();
				const row = cell.getRow();
				const data = row.getData();

				if (data.isBridge) {
					const name = data.description || value;
					return renderWithIcon('clarity:network-switch-line', name);
				}

				if (value === 'lo0') {
					return renderWithIcon('ic:baseline-loop', value);
				}

				if (data.isEpair) {
					const jail = jails.find((jail) =>
						jail?.networks?.some((net) =>
							net?.macObj?.entries?.some(
								(entry) =>
									(data?.ether && isMACNearOrEqual(entry.value, data.ether)) ||
									(data?.hwaddr && isMACNearOrEqual(entry.value, data.hwaddr))
							)
						)
					);

					const jn = jail ? `(${jail.name}) ${value}` : value;
					return renderWithIcon('raphael:ethernet', jn);
				}

				if (data.isTap) {
					const vm = vms.find((vm) =>
						vm?.networks?.some((net) =>
							net?.macObj?.entries?.some(
								(entry) =>
									(data?.ether && isMACNearOrEqual(entry.value, data.ether, true)) ||
									(data?.hwaddr && isMACNearOrEqual(entry.value, data.hwaddr, true))
							)
						)
					);

					return renderWithIcon('temaki:water-tap', vm ? `(${vm.name}) ${value}` : value);
				}

				return renderWithIcon('mdi:ethernet', value);
			}
		},
		{
			field: 'model',
			title: 'Model'
		},
		{
			field: 'description',
			title: 'Description',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value) {
					return value;
				}

				return '-';
			}
		},
		{
			field: 'ether',
			title: 'MAC Address',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow();
				const hwAddr = row.getData().hwaddr;
				const macAddr = cell.getValue();
				const isEpair = row.getData().isEpair;

				if (hwAddr && hwAddr !== macAddr && isEpair) {
					return row.getData().hwaddr;
				}

				return macAddr || '-';
			}
		},
		{
			field: 'metric',
			title: 'Metric'
		},
		{
			field: 'mtu',
			title: 'MTU'
		},
		{
			field: 'media',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				const status = value?.status || '-';
				if (status === 'active') {
					return 'Active';
				}

				return status;
			}
		},
		{
			field: 'isBridge',
			title: 'isBridge',
			visible: false
		},
		{
			field: 'isEpair',
			title: 'isEpair',
			visible: false
		}
	]);

	let tableData = $derived(generateTableData(columns, results[0].data as Iface[]));
	let activeRow: Row[] | null = $state(null);
	let query: string = $state('');
	let viewModal = $state({
		title: '',
		key: 'Attribute',
		value: 'Value',
		open: false,
		KV: {},
		type: 'kv',
		actions: {
			close: () => {
				viewModal.open = false;
			}
		}
	});

	function viewInterface(iface: string) {
		const ifaceData = results[0].data?.find((i: Iface) => i.name === iface);
		if (ifaceData) {
			viewModal.KV = getCleanIfaceData(ifaceData);
			viewModal.title = `Details - ${ifaceData.name}`;
			viewModal.open = true;
		}
	}
</script>

{#snippet button(type: string)}
	{#if type === 'view' && activeRow !== null && activeRow.length > 0}
		<Button
			onclick={() => activeRow !== null && viewInterface(activeRow[0]?.name)}
			size="sm"
			variant="outline"
			class="h-6.5"
		>
			<span class="icon-[mdi--eye] mr-1 h-4 w-4"></span>

			{'View'}
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		{@render button('view')}
	</div>

	<KvTableModal
		titles={{
			icon: 'carbon--network-interface',
			main: viewModal.title,
			key: viewModal.key,
			value: viewModal.value
		}}
		open={viewModal.open}
		KV={viewModal.KV}
		type={viewModal.type}
		actions={viewModal.actions}
	></KvTableModal>

	<TreeTable
		data={tableData}
		name="tt-networkInterfaces"
		multipleSelect={false}
		bind:parentActiveRow={activeRow}
		bind:query
	/>
</div>
