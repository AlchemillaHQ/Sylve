<script lang="ts">
	import {
		getDHCPRanges,
		getLeases,
		deleteDHCPLease,
		deleteDynamicDHCPLease
	} from '$lib/api/network/dhcp';
	import CreateOrEdit from '$lib/components/custom/Network/DHCP/Lease/CreateOrEdit.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import {
		emptyLeases,
		type DHCPStaticLease,
		type DHCPRange,
		type FileLease,
		type Leases,
		type DHCPLeaseRow
	} from '$lib/types/network/dhcp';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import { getNetworkObjects } from '$lib/api/network/object';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { escapeHTML, generateNanoId } from '$lib/utils/string';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { secondsToHoursAgo } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { toast } from 'svelte-sonner';
	import { resource, watch } from 'runed';
	import { type APIResponse } from '$lib/types/common';

	interface Data {
		dhcpRanges: DHCPRange[] | APIResponse;
		dhcpLeases: Leases | APIResponse;
		networkObjects: NetworkObject[] | APIResponse;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let lastGoodNetworkObjects = Array.isArray(data.networkObjects)
		? data.networkObjects
		: ([] as NetworkObject[]);
	// svelte-ignore state_referenced_locally
	let lastGoodDHCPRanges = Array.isArray(data.dhcpRanges) ? data.dhcpRanges : ([] as DHCPRange[]);
	// svelte-ignore state_referenced_locally
	let lastGoodDHCPLeases = isAPIResponse(data.dhcpLeases) ? emptyLeases() : data.dhcpLeases;

	let dhcpRanges = resource(
		() => 'dhcp-ranges',
		async (key) => {
			const res = await getDHCPRanges();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodDHCPRanges;
			}

			lastGoodDHCPRanges = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodDHCPRanges }
	);

	let dhcpLeases = resource(
		() => 'dhcp-leases',
		async (key) => {
			const res = await getLeases();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodDHCPLeases;
			}

			lastGoodDHCPLeases = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodDHCPLeases }
	);

	let networkObjects = resource(
		() => 'network-objects',
		async (key) => {
			const res = await getNetworkObjects();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodNetworkObjects;
			}

			lastGoodNetworkObjects = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodNetworkObjects }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
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
			type: '' as 'static' | 'dynamic' | '',
			id: 0,
			ip: '',
			identifier: '',
			display: ''
		}
	});

	function resetDeleteModal() {
		modals.delete.open = false;
		modals.delete.type = '';
		modals.delete.id = 0;
		modals.delete.ip = '';
		modals.delete.identifier = '';
		modals.delete.display = '';
	}

	function normalizeLeaseValue(value: string | null | undefined): string {
		return value?.trim().toLowerCase() ?? '';
	}

	function staticLeaseMatchesFile(staticLease: DHCPStaticLease, fileLease: FileLease): boolean {
		const fileIP = normalizeLeaseValue(fileLease.ip);
		const hasMatchingIP =
			staticLease.ipObject?.entries?.some((entry) => normalizeLeaseValue(entry.value) === fileIP) ??
			false;
		if (!hasMatchingIP) return false;

		if (fileLease.mac) {
			const fileMAC = normalizeLeaseValue(fileLease.mac);
			return (
				staticLease.macObject?.entries?.some(
					(entry) => normalizeLeaseValue(entry.value) === fileMAC
				) ?? false
			);
		}

		if (fileLease.duid) {
			const fileDUID = normalizeLeaseValue(fileLease.duid);
			return (
				staticLease.duidObject?.entries?.some(
					(entry) => normalizeLeaseValue(entry.value) === fileDUID
				) ?? false
			);
		}

		return false;
	}

	let query = $state('');
	let activeRows: DHCPLeaseRow[] | null = $state(null);
	let activeRow: DHCPLeaseRow | null = $derived(
		activeRows ? (activeRows[0] as DHCPLeaseRow) : ({} as DHCPLeaseRow)
	);

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
			const found = dhcpLeases.current.db.find((lease) => staticLeaseMatchesFile(lease, entry));

			if (found) {
				const row = rows.find((candidate) => candidate.dbId === found.id.toString());

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
					identifier: entry.mac ? entry.mac.toLowerCase() : entry.duid?.toLowerCase() || '-',
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
						modals.delete.id = activeRow?.dbId || 0;
						modals.delete.display = activeRow?.hostname || activeRow?.ip || '';
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
						modals.edit.id = activeRow?.dbId || 0;
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
					modals.delete.display = activeRow?.hostname || activeRow?.ip || '';
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

{#if modals.create.open}
	<CreateOrEdit
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
	keepOpenOnConfirm={true}
	customTitle={`This action cannot be undone. This will permanently delete ${modals.delete.type} DHCP lease for <b>${escapeHTML(modals.delete.display)}</b>`}
	actions={{
		onConfirm: async () => {
			let result = null as null | APIResponse;

			if (modals.delete.type === 'static') {
				result = await deleteDHCPLease(modals.delete.id);
			} else if (modals.delete.type === 'dynamic') {
				result = await deleteDynamicDHCPLease(modals.delete.identifier, modals.delete.ip);
			}

			if (result === null) {
				toast.error('Invalid DHCP lease type', { position: 'bottom-center' });
				return;
			}

			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to delete DHCP lease', { position: 'bottom-center' });
				return;
			}

			toast.success('DHCP lease deleted', { position: 'bottom-center' });
			reload = true;
			resetDeleteModal();
			activeRows = null;
		},
		onCancel: resetDeleteModal
	}}
	loadingLabel="Deleting Lease..."
></AlertDialog>
