<script lang="ts">
	import { deleteDHCPRange, getDHCPConfig, getDHCPRanges } from '$lib/api/network/dhcp';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CreateOrEdit from '$lib/components/custom/Network/DHCP/Range/CreateOrEdit.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { DHCPConfig, DHCPRange } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { secondsToDnsmasq } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
	}

	let { data }: { data: Data } = $props();

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
	let dhcpConfig = resource(
		() => 'dhcp-config',
		async (key) => {
			const res = await getDHCPConfig();
			if (isAPIResponse(res)) {
				return data.dhcpConfig;
			}
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
			if (isAPIResponse(res)) {
				return data.dhcpRanges;
			}
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpRanges }
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
			id: 0
		}
	});

	let query = $state('');
	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'type',
				title: 'Type',
				formatter(cell) {
					if (cell.getValue() === 'ipv4') {
						return 'IPv4';
					} else if (cell.getValue() === 'ipv6') {
						return 'IPv6';
					}
				}
			},
			{
				field: 'sw',
				title: 'Switch'
			},
			{
				field: 'startIP',
				title: 'Start IP',
				formatter(cell) {
					if (cell.getValue() === '') {
						return '-';
					} else {
						return cell.getValue();
					}
				}
			},
			{
				field: 'endIP',
				title: 'End IP',
				formatter(cell) {
					if (cell.getValue() === '') {
						return '-';
					} else {
						return cell.getValue();
					}
				}
			},
			{
				field: 'expiry',
				title: 'Expiry',
				formatter(cell) {
					if (cell.getValue() === 0) {
						return renderWithIcon('mdi:forever', 'Never');
					} else {
						return secondsToDnsmasq(cell.getValue());
					}
				}
			}
		];

		const rows: Row[] = [];

		if (!dhcpRanges || dhcpRanges.current.length === 0) {
			return {
				columns,
				rows
			};
		}

		for (const range of dhcpRanges.current) {
			let swName = 'N/A';
			if (range.standardSwitch) {
				swName = range.standardSwitch.name;
			} else if (range.manualSwitch) {
				swName = range.manualSwitch.name;
			}

			rows.push({
				id: range.id,
				type: range.type,
				sw: swName,
				startIP: range.startIp,
				endIP: range.endIp,
				expiry: range.expiry
			});
		}

		return { columns, rows };
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let selectedRange = $derived(
		dhcpRanges && activeRow
			? dhcpRanges.current.find((r) => r.id === Number(activeRow.id)) || null
			: null
	);
</script>

{#snippet button(type: 'delete' | 'edit')}
	<Button
		onclick={() => {
			modals[type].open = !modals[type].open;
			modals[type].id = Number(activeRow?.id);
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
	>
		{#if type === 'delete'}
			<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
		{:else if type === 'edit'}
			<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
		{/if}
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{#if activeRows && activeRows.length === 1}
			{@render button('edit')}
			{@render button('delete')}
		{/if}
	</div>

	<TreeTable
		data={tableData}
		{query}
		name="tt-dhcp-ranges"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

{#if modals.create.open}
	<CreateOrEdit
		bind:open={modals.create.open}
		bind:reload
		networkInterfaces={networkInterfaces.current}
		networkSwitches={networkSwitches.current}
		dhcpConfig={dhcpConfig.current}
		selectedRange={null}
		dhcpRanges={dhcpRanges.current}
	/>
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		bind:open={modals.edit.open}
		bind:reload
		networkInterfaces={networkInterfaces.current}
		networkSwitches={networkSwitches.current}
		dhcpConfig={dhcpConfig.current}
		{selectedRange}
		dhcpRanges={dhcpRanges.current}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'DHCP Range', element: activeRow?.id.toString() || '' }}
	customTitle="This action cannot be undone. This will permanently delete this DHCP range including all leases/options associated with it"
	actions={{
		onConfirm: async () => {
			const result = await deleteDHCPRange(modals.delete.id);
			reload = true;
			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to delete DHCP range', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success('DHCP range deleted', {
					position: 'bottom-center'
				});
				modals.delete.open = false;
				modals.delete.id = 0;
				activeRows = null;
			}
		},
		onCancel: () => {
			modals.delete.open = false;
			modals.delete.id = 0;
			activeRows = null;
		}
	}}
></AlertDialog>
