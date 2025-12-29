<script lang="ts">
	import { getDHCPConfig, getDHCPRanges, getLeases, deleteDHCPLease } from '$lib/api/network/dhcp';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import CreateOrEdit from '$lib/components/custom/Network/DHCP/Lease/CreateOrEdit.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import type { DHCPConfig, DHCPRange, Leases } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import { getNetworkObjects } from '$lib/api/network/object';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { generateNanoId } from '$lib/utils/string';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { secondsToHoursAgo } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { toast } from 'svelte-sonner';
	import { resource, watch } from 'runed';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		dhcpLeases: Leases;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key, prevKey, { signal }) => {
			const res = await getInterfaces();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	let networkSwitches = resource(
		() => 'network-switches',
		async (key, prevKey, { signal }) => {
			const res = await getSwitches();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.switches }
	);

	let dhcpConfig = resource(
		() => 'dhcp-config',
		async (key, prevKey, { signal }) => {
			const res = await getDHCPConfig();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpConfig }
	);

	let dhcpRanges = resource(
		() => 'dhcp-ranges',
		async (key, prevKey, { signal }) => {
			const res = await getDHCPRanges();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpRanges }
	);

	let dhcpLeases = resource(
		() => 'dhcp-leases',
		async (key, prevKey, { signal }) => {
			const res = await getLeases();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpLeases }
	);

	let networkObjects = resource(
		() => 'network-objects',
		async (key, prevKey, { signal }) => {
			const res = await getNetworkObjects();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.networkObjects }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
				networkInterfaces.refetch();
				networkSwitches.refetch();
				dhcpConfig.refetch();
				dhcpRanges.refetch();
				dhcpLeases.refetch();
				networkObjects.refetch();
				reload = false;
			}
		}
	);

	let modals = $state({
		create: {
			open: false
		},
		edit: {
			open: false,
			id: 0
		},
		delete: {
			open: false,
			id: '0'
		}
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{
				field: 'dbId',
				title: 'dbId',
				visible: false
			},
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'type',
				title: 'Type',
				formatter: (cell) => {
					const value = cell.getValue();
					if (value === 'static') {
						return renderWithIcon('mdi:lock', 'Static');
					} else if (value === 'dynamic') {
						return renderWithIcon('mdi:autorenew', 'Dynamic');
					} else {
						return '-';
					}
				},
				width: '10%'
			},
			{
				field: 'identifier',
				title: 'Identifier',
				copyOnClick: true
			},
			{
				field: 'hostname',
				title: 'Hostname'
			},
			{
				field: 'ip',
				title: 'IP Address',
				copyOnClick: true
			},
			{
				field: 'range',
				title: 'Range'
			},
			{
				field: 'switch',
				title: 'Switch'
			},
			{
				field: 'expiry',
				title: 'Expiry',
				formatter(cell) {
					const value = cell.getValue();
					if (value === 'never') {
						return renderWithIcon('mdi:forever', 'Never');
					} else if (value) {
						return secondsToHoursAgo(value);
					} else {
						return '-';
					}
				}
			}
		];
		const rows: Row[] = [];

		for (const entry of dhcpLeases.current.db) {
			const range = `${entry.dhcpRange?.startIp} - ${entry.dhcpRange?.endIp}`;
			const sw = entry.dhcpRange?.standardSwitchId
				? entry.dhcpRange?.standardSwitch?.name
				: entry.dhcpRange?.manualSwitch?.name;

			const ip = entry.ipObject?.entries ? entry.ipObject?.entries[0]?.value : '-';
			const mac = entry.macObject?.entries ? entry.macObject?.entries[0]?.value : '-';
			const duid = entry.duidObject?.entries ? entry.duidObject?.entries[0]?.value : '-';

			rows.push({
				dbId: entry.id.toString(),
				id: generateNanoId(`${entry.hostname}-${entry.dhcpRangeId}-db`),
				identifier: mac !== '-' ? mac : duid,
				hostname: entry.hostname,
				ip: ip,
				range: range,
				mac: mac,
				duid: duid,
				switch: sw,
				type: 'static',
				expiry: 'never'
			});
		}

		for (const entry of dhcpLeases.current.file) {
			const found = dhcpLeases.current.db.find((e) => {
				const ips = e.ipObject?.entries ? e.ipObject?.entries.map((i) => i.value) : [];
				return e.hostname === entry.hostname && ips.includes(entry.ip);
			});

			if (found) {
				const row = rows.find((r) => r.hostname === entry.hostname && r.ip === entry.ip);
				if (row) {
					row.expiry = 'never';
				}
				continue;
			} else {
				rows.push({
					id: generateNanoId(`${entry.hostname}-${entry.ip}-file`),
					hostname: entry.hostname,
					ip: entry.ip,
					range: '-',
					switch: '-',
					duid: entry.duid,
					mac: entry.mac,
					identifier: entry.mac ? entry.mac : entry.duid,
					expiry: entry.expiry === 0 ? 'never' : entry.expiry,
					type: 'dynamic'
				});
			}
		}

		return { columns, rows };
	});
</script>

{#snippet button(type: 'delete' | 'edit')}
	{#if activeRows !== null && activeRows.length === 1}
		{#if activeRow?.type === 'static'}
			{#if type === 'delete'}
				<Button
					onclick={() => {
						modals.delete.open = !modals.delete.open;
						modals.delete.id = activeRow?.dbId || '0';
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

						<span>Delete</span>
					</div>
				</Button>
			{:else if type === 'edit'}
				<Button
					onclick={() => {
						modals.edit.open = !modals.edit.open;
						modals.edit.id = activeRow?.dbId || '0';
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
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		{query}
		name="tt-dhcp-leases"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

{#if modals.create.open}
	<CreateOrEdit
		networkInterfaces={networkInterfaces.current}
		networkSwitches={networkSwitches.current}
		dhcpConfig={dhcpConfig.current}
		dhcpRanges={dhcpRanges.current}
		dhcpLeases={dhcpLeases.current}
		bind:reload
		networkObjects={networkObjects.current}
		bind:open={modals.create.open}
		selectedLease={null}
	/>
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		networkInterfaces={networkInterfaces.current}
		networkSwitches={networkSwitches.current}
		dhcpConfig={dhcpConfig.current}
		dhcpRanges={dhcpRanges.current}
		dhcpLeases={dhcpLeases.current}
		bind:reload
		networkObjects={networkObjects.current}
		bind:open={modals.edit.open}
		selectedLease={modals.edit.id}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	customTitle={`This action cannot be undone. This will permanently delete static DHCP lease for <b>${activeRow?.hostname || activeRow?.ip || ''}</b>`}
	actions={{
		onConfirm: async () => {
			const result = await deleteDHCPLease(parseInt(modals.delete.id));
			reload = true;
			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to delete DHCP lease', { position: 'bottom-center' });
				return;
			} else {
				toast.success('DHCP lease deleted', { position: 'bottom-center' });
			}

			modals.delete.open = false;
			modals.delete.id = '0';
			activeRows = null;
			activeRow = null;
		},
		onCancel: () => {
			modals.delete.open = false;
			modals.delete.id = '0';
		}
	}}
></AlertDialog>
