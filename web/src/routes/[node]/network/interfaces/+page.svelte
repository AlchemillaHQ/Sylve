<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getHostInterfaceL3, getHostInterfaceL3Pending } from '$lib/api/network/ifaceL3';
	import KvTableModal from '$lib/components/custom/KVTableModal.svelte';
	import HostInterfaceL3Form from '$lib/components/custom/Network/IfaceL3/Form.svelte';
	import HostInterfaceL3PendingDialog from '$lib/components/custom/Network/IfaceL3/Pending.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column } from '$lib/types/components/tree-table';
	import { type Iface, type IfaceRow } from '$lib/types/network/iface';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateTableData, getCleanIfaceData } from '$lib/utils/network/iface';
	import { hostInterfaceL3Label, hostInterfaceL3Labels } from '$lib/utils/network/ifaceL3';
	import { renderWithIcon } from '$lib/utils/table';
	import type { CellComponent } from 'tabulator-tables';
	import type { Jail } from '$lib/types/jail/jail';
	import { getJailsResult } from '$lib/api/jail/jail';
	import { isMACNearOrEqual } from '$lib/utils/mac';
	import type { VM } from '$lib/types/vm/vm';
	import { getVMsResult } from '$lib/api/vm/vm';
	import { getSwitches } from '$lib/api/network/switch';
	import { emptySwitchList, isSwitchList, type SwitchList } from '$lib/types/network/switch';
	import { getWireGuardClients } from '$lib/api/network/wireguard';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import {
		emptyHostInterfaceL3List,
		isHostInterfaceL3List,
		type HostInterfaceL3Entry,
		type HostInterfaceL3List,
		type HostInterfaceL3PendingEntry
	} from '$lib/types/network/ifaceL3';
	import { resource } from 'runed';

	interface Data {
		interfaces: Iface[] | APIResponse;
		jails: Jail[];
		vms: VM[];
		switches: SwitchList | APIResponse;
		wgClients: WireGuardClient[] | APIResponse;
		l3: HostInterfaceL3List | APIResponse;
		pending: HostInterfaceL3PendingEntry[] | APIResponse;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let lastGoodInterfaces = Array.isArray(data.interfaces) ? data.interfaces : ([] as Iface[]);
	// svelte-ignore state_referenced_locally
	let lastGoodSwitches = isSwitchList(data.switches) ? data.switches : emptySwitchList();
	let initialWireGuardClients = $derived(
		Array.isArray(data.wgClients) ? data.wgClients : ([] as WireGuardClient[])
	);
	// svelte-ignore state_referenced_locally
	let lastGoodHostInterfaceL3 = isHostInterfaceL3List(data.l3)
		? data.l3
		: emptyHostInterfaceL3List();
	// svelte-ignore state_referenced_locally
	const initialHostInterfaceL3Pending = Array.isArray(data.pending) ? data.pending : [];

	let networkSwitches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			if (!isSwitchList(res)) {
				handleAPIError(res);
				return lastGoodSwitches;
			}
			lastGoodSwitches = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodSwitches }
	);

	// svelte-ignore state_referenced_locally
	let wgClients = resource(
		() => 'network-vpn-wireguard-clients',
		async (key) => {
			const res = await getWireGuardClients();
			if (isAPIResponse(res)) {
				return initialWireGuardClients;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: initialWireGuardClients }
	);

	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodInterfaces;
			}
			lastGoodInterfaces = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodInterfaces }
	);

	let hostInterfaceL3Resource = resource(
		() => 'network-interface-l3',
		async (key) => {
			const res = await getHostInterfaceL3();
			if (!isHostInterfaceL3List(res)) {
				handleAPIError(res);
				return lastGoodHostInterfaceL3;
			}
			lastGoodHostInterfaceL3 = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodHostInterfaceL3 }
	);

	let hostInterfaceL3 = $derived(hostInterfaceL3Resource.current);
	let hostInterfaceL3ReadOnlyReason = $derived.by(() => {
		const selectedNode = String(page.params.node ?? '')
			.trim()
			.toLowerCase();
		const localNode = String(storage.localHostname ?? '')
			.trim()
			.toLowerCase();
		if (selectedNode !== '' && localNode !== '' && selectedNode !== localNode) {
			return `Host IP changes must be made on ${localNode}.`;
		}
		return '';
	});

	// svelte-ignore state_referenced_locally
	let jails = resource(
		() => 'jail-list',
		async (key) => {
			const res = await getJailsResult();
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
			const res = await getVMsResult();
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
			field: 'l3Managed',
			title: 'Host IP',
			formatter(cell: CellComponent) {
				const data = cell.getRow().getData();
				if (!data.l3Managed) {
					return '-';
				}
				if (data.l3Orphan) {
					return renderWithIcon('mdi:help-circle-outline', 'Interface missing');
				}
				const labels = hostInterfaceL3Labels(data.l3Conflicts as string[]);
				if (labels.length > 0) {
					return renderWithIcon('mdi:alert-outline', labels.join('; '));
				}
				return renderWithIcon('mdi:ip', 'Managed');
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

	let tableData = $derived(
		generateTableData(columns, networkInterfaces.current, hostInterfaceL3.rows)
	);
	let activeRow: IfaceRow[] | null = $state(null);
	let query: string = $state('');
	let refreshing = $state(false);
	let l3Targets = $derived(
		new Map(hostInterfaceL3.targets.map((target) => [target.interface, target]))
	);

	let hostInterfaceL3Modal = $state<{
		open: boolean;
		name: string;
		mtu: number;
		entry: HostInterfaceL3Entry | null;
	}>({ open: false, name: '', mtu: 1500, entry: null });

	let hostInterfaceL3Pending = $state<{
		open: boolean;
		entries: HostInterfaceL3PendingEntry[];
	}>({ open: initialHostInterfaceL3Pending.length > 0, entries: initialHostInterfaceL3Pending });

	async function refreshAfterHostInterfaceL3Change() {
		await Promise.all([hostInterfaceL3Resource.refetch(), networkInterfaces.refetch()]);
	}

	async function refreshHostInterfacePage() {
		refreshing = true;
		try {
			await Promise.all([refreshAfterHostInterfaceL3Change(), refreshPendingQueue()]);
		} finally {
			refreshing = false;
		}
	}

	function openHostInterfaceL3Modal() {
		const name = activeRow?.[0]?.name;
		if (!name) {
			return;
		}
		const live = networkInterfaces.current.find((iface: Iface) => iface.name === name);
		const entry = hostInterfaceL3.rows.find((row) => row.interface === name) ?? null;
		hostInterfaceL3Modal = {
			open: true,
			name,
			mtu: live?.mtu ?? entry?.mtu ?? 1500,
			entry
		};
	}

	async function refreshPendingQueue(fallback?: HostInterfaceL3PendingEntry) {
		const response = await getHostInterfaceL3Pending();
		if (isAPIResponse(response)) {
			const entries = fallback
				? [fallback, ...hostInterfaceL3Pending.entries.filter((entry) => entry.id !== fallback.id)]
				: hostInterfaceL3Pending.entries;
			hostInterfaceL3Pending = {
				open: fallback !== undefined || hostInterfaceL3Pending.open,
				entries
			};
			return;
		}
		hostInterfaceL3Pending = { open: response.length > 0, entries: response };
	}

	async function handleHostInterfaceL3Pending(entry: HostInterfaceL3PendingEntry) {
		await hostInterfaceL3Resource.refetch();
		await refreshPendingQueue(entry);
	}

	async function handleHostInterfaceL3Done(reloadForm = false) {
		await refreshAfterHostInterfaceL3Change();
		await refreshPendingQueue();
		if (reloadForm && hostInterfaceL3Modal.open) {
			const name = hostInterfaceL3Modal.name;
			const live = networkInterfaces.current.find((iface: Iface) => iface.name === name);
			const entry = hostInterfaceL3.rows.find((row) => row.interface === name) ?? null;
			hostInterfaceL3Modal = {
				open: true,
				name,
				mtu: live?.mtu ?? entry?.mtu ?? hostInterfaceL3Modal.mtu,
				entry
			};
		}
	}

	let viewModal = $state({
		title: '',
		open: false,
		KV: {},
		type: 'kv'
	});
	let viewModalLabels = $derived({ key: 'Attribute', value: 'Value' });

	function viewInterface(iface: string) {
		const ifaceData = networkInterfaces.current.find((i: Iface) => i.name === iface);
		if (ifaceData) {
			viewModal.KV = getCleanIfaceData(ifaceData);
			viewModal.title = `Details - ${ifaceData.name}`;
			viewModal.open = true;
			return;
		}

		const entry = hostInterfaceL3.rows.find((row) => row.interface === iface);
		if (entry) {
			viewModal.KV = {
				Name: entry.interface,
				Status: 'Interface missing',
				Addresses:
					entry.addresses
						.map((address) => `${address.address}/${address.prefixLength}`)
						.join(', ') || '-',
				MTU: entry.mtu ?? '-',
				Metric: entry.metric ?? '-',
				'IPv6 mode': entry.ipv6Mode,
				Conflicts: hostInterfaceL3Labels(entry.conflicts).join(', ') || '-'
			};
			viewModal.title = `Details - ${entry.interface}`;
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
	{:else if type === 'hostIp' && activeRow !== null && activeRow.length > 0}
		{@const target = l3Targets.get(activeRow[0]?.name)}
		{@const entry = hostInterfaceL3.rows.find((row) => row.interface === activeRow?.[0]?.name)}
		{@const available = entry !== undefined || target?.eligible === true}
		<Button
			onclick={openHostInterfaceL3Modal}
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!available}
			title={available
				? hostInterfaceL3ReadOnlyReason || 'Host IP'
				: hostInterfaceL3Label(target?.reason ?? '') ||
					'Host IP is not available for this interface'}
		>
			<SpanWithIcon icon="icon-[mdi--ip]" size="h-4 w-4" gap="gap-2" title="Host IP" />
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			onclick={refreshHostInterfacePage}
			disabled={refreshing}
		>
			<SpanWithIcon
				icon={refreshing ? 'icon-[mdi--loading]' : 'icon-[mdi--refresh]'}
				size={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`}
				gap="gap-2"
				title="Refresh"
			/>
		</Button>
		{@render button('view')}
		{@render button('hostIp')}
		{#if hostInterfaceL3Pending.entries.length > 0}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => (hostInterfaceL3Pending.open = true)}
			>
				<SpanWithIcon
					icon="icon-[mdi--timer-alert-outline]"
					size="h-4 w-4"
					gap="gap-2"
					title={`Pending Host IP (${hostInterfaceL3Pending.entries.length})`}
				/>
			</Button>
		{/if}
	</div>

	<KvTableModal
		titles={{
			icon: 'carbon--network-interface',
			main: viewModal.title,
			key: viewModalLabels.key,
			value: viewModalLabels.value
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

	<HostInterfaceL3Form
		bind:open={hostInterfaceL3Modal.open}
		interfaceName={hostInterfaceL3Modal.name}
		currentMTU={hostInterfaceL3Modal.mtu}
		entry={hostInterfaceL3Modal.entry}
		liveIPv4={networkInterfaces.current.find(
			(iface: Iface) => iface.name === hostInterfaceL3Modal.name
		)?.ipv4 ?? []}
		interfaceMissing={!networkInterfaces.current.some(
			(iface: Iface) => iface.name === hostInterfaceL3Modal.name
		)}
		readOnlyReason={hostInterfaceL3ReadOnlyReason}
		onDone={handleHostInterfaceL3Done}
		onPending={handleHostInterfaceL3Pending}
		onReviewPending={() => (hostInterfaceL3Pending.open = true)}
	/>

	<HostInterfaceL3PendingDialog
		bind:open={hostInterfaceL3Pending.open}
		entry={hostInterfaceL3Pending.entries[0] ?? null}
		remaining={hostInterfaceL3Pending.entries.length}
		readOnlyReason={hostInterfaceL3ReadOnlyReason}
		onDone={handleHostInterfaceL3Done}
	/>
</div>
