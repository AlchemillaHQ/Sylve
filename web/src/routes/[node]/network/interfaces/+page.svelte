<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import KvTableModal from '$lib/components/custom/KVTableModal.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { type Iface } from '$lib/types/network/iface';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateTableData, getCleanIfaceData } from '$lib/utils/network/iface';
	import { renderWithIcon } from '$lib/utils/table';
	import type { CellComponent } from 'tabulator-tables';
	import type { Jail } from '$lib/types/jail/jail';
	import { getJails } from '$lib/api/jail/jail';
	import { isMACNearOrEqual } from '$lib/utils/mac';
	import type { VM } from '$lib/types/vm/vm';
	import { getVMs } from '$lib/api/vm/vm';
	import { getSwitches } from '$lib/api/network/switch';
	import type { SwitchList } from '$lib/types/network/switch';
	import { getWireGuardClients } from '$lib/api/network/wireguard';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { resource } from 'runed';

	interface Data {
		interfaces: Iface[];
		jails: Jail[];
		vms: VM[];
		switches: SwitchList;
		wgClients: WireGuardClient[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let networkSwitches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			if (isAPIResponse(res)) {
				return data.switches;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.switches }
	);

	// svelte-ignore state_referenced_locally
	let wgClients = resource(
		() => 'network-vpn-wireguard-clients',
		async (key) => {
			const res = await getWireGuardClients();
			if (isAPIResponse(res)) {
				return data.wgClients;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.wgClients }
	);

	// svelte-ignore state_referenced_locally
	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			if (isAPIResponse(res)) {
				return data.interfaces;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	// svelte-ignore state_referenced_locally
	let jails = resource(
		() => 'jail-list',
		async (key) => {
			const res = await getJails();
			if (isAPIResponse(res)) {
				return data.jails;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.jails }
	);

	// svelte-ignore state_referenced_locally
	let vms = resource(
		() => 'vm-list',
		async (key) => {
			const res = await getVMs();
			if (isAPIResponse(res)) {
				return data.vms;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.vms }
	);

	let columns: Column[] = [
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
					const switches = networkSwitches.current;
					const manualSwitch = switches?.manual?.find((sw) => sw.bridge === value);
					const standardSwitch = switches?.standard?.find((sw) => sw.bridgeName === value);
					const name = manualSwitch?.name || standardSwitch?.name || data.description || value;
					return renderWithIcon('clarity:network-switch-line', name);
				}

				if (value === 'wgs0') {
					return renderWithIcon('mdi:vpn', 'WireGuard Server');
				}

				const wgcMatch = /^wgc(\d+)$/.exec(value);
				if (wgcMatch) {
					const clientId = parseInt(wgcMatch[1]);
					const clients = wgClients.current;
					const client = Array.isArray(clients)
						? clients.find((c) => c.id === clientId)
						: undefined;
					const label = client
						? `${client.name} (WireGuard Client)`
						: `${value} (WireGuard Client)`;
					return renderWithIcon('mdi:vpn', label);
				}

				if (value === 'lo0') {
					return renderWithIcon('ic:baseline-loop', value);
				}

				if (data.isEpair) {
					const jail = jails.current.find((jail) =>
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
					const vm = vms.current.find((vm) =>
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
			field: 'ipv4',
			title: 'IPv4',
			formatter: 'textarea'
		},
		{
			field: 'ipv6',
			title: 'IPv6',
			formatter: 'textarea'
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
	];

	let tableData = $derived(generateTableData(columns, networkInterfaces.current));
	let activeRow: Row[] | null = $state(null);
	let query: string = $state('');

	let viewModal = $state({
		title: '',
		key: 'Attribute',
		value: 'Value',
		open: false,
		KV: {},
		type: 'kv'
	});

	function viewInterface(iface: string) {
		const ifaceData = networkInterfaces.current.find((i: Iface) => i.name === iface);
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
			<SpanWithIcon icon="icon-[mdi--eye]" size="h-4 w-4" gap="gap-2" title="View" />
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
		bind:open={viewModal.open}
		KV={viewModal.KV}
	></KvTableModal>

	<TreeTable
		data={tableData}
		name="tt-networkInterfaces"
		multipleSelect={false}
		bind:parentActiveRow={activeRow}
		bind:query
	/>
</div>
