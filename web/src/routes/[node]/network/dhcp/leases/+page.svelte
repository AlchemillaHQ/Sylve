<script lang="ts">
	import {
		getDHCPConfig,
		getDHCPRanges,
		getLeases,
		deleteDHCPLease,
		deleteDynamicDHCPLease
	} from '$lib/api/network/dhcp';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import CreateOrEdit from '$lib/components/custom/Network/DHCP/Lease/CreateOrEdit.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import type { DHCPConfig, DHCPRange, Leases } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
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
	import { type APIResponse } from '$lib/types/common';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		dhcpLeases: Leases;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	// svelte-ignore state_referenced_locally
	let networkSwitches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.switches }
	);

	// svelte-ignore state_referenced_locally
	let dhcpConfig = resource(
		() => 'dhcp-config',
		async (key) => {
			const res = await getDHCPConfig();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpConfig }
	);

	// svelte-ignore state_referenced_locally
	let dhcpRanges = resource(
		() => 'dhcp-ranges',
		async (key) => {
			const res = await getDHCPRanges();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpRanges }
	);

	// svelte-ignore state_referenced_locally
	let dhcpLeases = resource(
		() => 'dhcp-leases',
		async (key) => {
			const res = await getLeases();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpLeases }
	);

	// svelte-ignore state_referenced_locally
	let networkObjects = resource(
		() => 'network-objects',
		async (key) => {
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
			loading: false,
			type: '' as 'static' | 'dynamic' | '',
			id: '0',
			ip: '',
			identifier: ''
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
				title: 'Hostname',
				formatter(cell) {
					const value = cell.getValue();
					return value || '-';
				}
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
			const macRaw = entry.macObject?.entries ? entry.macObject?.entries[0]?.value : '-';
			const mac = macRaw !== '-' ? macRaw.toLowerCase() : '-';
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
				const isIpMatch = ips.includes(entry.ip);
				if (!isIpMatch) return false;

				const dbHostname = e.hostname?.toLowerCase();
				const dbMac = e.macObject?.entries?.[0]?.value?.toLowerCase();
				const fileHostname = entry.hostname?.toLowerCase();
				const fileMac = entry.mac?.toLowerCase();
				const isHostnameMatch = dbHostname && fileHostname && dbHostname === fileHostname;
				const isMacMatch = dbMac && fileMac && dbMac === fileMac;

				return isHostnameMatch || isMacMatch;
			});

			if (found) {
				const row = rows.find(
					(r) =>
						r.ip === entry.ip &&
						(r.hostname?.toLowerCase() === entry.hostname?.toLowerCase() ||
							r.mac?.toLowerCase() === entry.mac?.toLowerCase())
				);

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
					mac: entry.mac ? entry.mac.toLowerCase() : '-',
					identifier: entry.mac ? entry.mac.toLowerCase() : entry.duid,
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
						modals.delete.type = 'static';
						modals.delete.id = activeRow?.dbId || '0';
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
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
					<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
				</Button>
			{/if}
		{/if}

		{#if activeRow?.type === 'dynamic' && type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = !modals.delete.open;
					modals.delete.type = 'dynamic';
					modals.delete.identifier = activeRow?.identifier || '';
					modals.delete.ip = activeRow?.ip || '';
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
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

{#if modals.create.open && !isAPIResponse(networkObjects.current)}
	<CreateOrEdit
		dhcpRanges={dhcpRanges.current}
		dhcpLeases={dhcpLeases.current}
		bind:reload
		networkObjects={networkObjects.current}
		bind:open={modals.create.open}
		selectedLease={null}
	/>
{/if}

{#if modals.edit.open && !isAPIResponse(networkObjects.current)}
	<CreateOrEdit
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
	customTitle={`This action cannot be undone. This will permanently delete ${modals.delete.type} DHCP lease for <b>${activeRow?.hostname || activeRow?.ip || ''}</b>`}
	actions={{
		onConfirm: async () => {
			let result = null as null | APIResponse;

			modals.delete.loading = true;

			if (modals.delete.type === 'static') {
				result = await deleteDHCPLease(parseInt(modals.delete.id));
			} else if (modals.delete.type === 'dynamic') {
				result = await deleteDynamicDHCPLease(modals.delete.identifier, modals.delete.ip);
			}

			if (result === null) {
				toast.error('Invalid DHCP lease type', { position: 'bottom-center' });
				return;
			}

			reload = true;
			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to delete DHCP lease', { position: 'bottom-center' });
				return;
			} else {
				toast.success('DHCP lease deleted', { position: 'bottom-center' });
			}

			modals.delete.open = false;
			modals.delete.loading = false;
			modals.delete.id = '0';
			modals.delete.identifier = '';
			modals.delete.ip = '';
			activeRows = null;
			activeRow = null;
		},
		onCancel: () => {
			modals.delete.open = false;
			modals.delete.id = '0';
			modals.delete.identifier = '';
			modals.delete.ip = '';
			modals.delete.loading = false;
		}
	}}
	loading={modals.delete.loading}
	loadingLabel="Deleting Lease..."
></AlertDialog>
