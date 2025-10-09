<script lang="ts">
	import { deleteDHCPRange, getDHCPConfig, getDHCPRanges } from '$lib/api/network/dhcp';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CreateOrEdit from '$lib/components/custom/Network/DHCP/Range/CreateOrEdit.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { DHCPConfig, DHCPRange } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { secondsToDnsmasq } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import Icon from '@iconify/svelte';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'network-interfaces',
			queryFn: async () => {
				return await getInterfaces();
			},
			keepPreviousData: true,
			initialData: data.interfaces,
			onSuccess: (data: Iface[]) => {
				updateCache('network-interfaces', data);
			}
		},
		{
			queryKey: 'network-switches',
			queryFn: async () => {
				return await getSwitches();
			},
			keepPreviousData: true,
			initialData: data.switches,
			onSuccess: (data: SwitchList) => {
				updateCache('network-switches', data);
			}
		},
		{
			queryKey: 'dhcp-config',
			queryFn: async () => {
				return await getDHCPConfig();
			},
			keepPreviousData: true,
			initialData: data.dhcpConfig,
			onSuccess: (data: DHCPConfig) => {
				updateCache('dhcp-config', data);
			}
		},
		{
			queryKey: 'dhcp-ranges',
			queryFn: async () => {
				return await getDHCPRanges();
			},
			keepPreviousData: true,
			initialData: data.dhcpRanges,
			onSuccess: (data: DHCPRange[]) => {
				updateCache('dhcp-ranges', data);
			}
		}
	]);

	let networkInterfaces = $derived($results[0].data as Iface[]);
	let networkSwitches = $derived($results[1].data as SwitchList);
	let dhcpConfig = $derived($results[2].data as DHCPConfig);
	let dhcpRanges = $derived($results[3].data as DHCPRange[]);
	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.invalidateQueries('network-interfaces');
			queryClient.invalidateQueries('network-switches');
			queryClient.invalidateQueries('dhcp-config');
			queryClient.invalidateQueries('dhcp-ranges');
			reload = false;
		}
	});

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
				field: 'sw',
				title: 'Switch'
			},
			{
				field: 'startIP',
				title: 'Start IP'
			},
			{ field: 'endIP', title: 'End IP' },
			{
				field: 'expiry',
				title: 'Expiry',
				formatter(cell, formatterParams, onRendered) {
					if (cell.getValue() === 0) {
						return renderWithIcon('mdi:forever', 'Never');
					} else {
						return secondsToDnsmasq(cell.getValue());
					}
				}
			}
		];
		const rows: Row[] = [];

		if (!dhcpRanges || dhcpRanges.length === 0) {
			return {
				columns,
				rows
			};
		}

		for (const range of dhcpRanges) {
			let swName = 'N/A';
			if (range.standardSwitch) {
				swName = range.standardSwitch.name;
			} else if (range.manualSwitch) {
				swName = range.manualSwitch.name;
			}

			rows.push({
				id: range.id,
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
		dhcpRanges && activeRow ? dhcpRanges.find((r) => r.id === Number(activeRow.id)) || null : null
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
		<div class="flex items-center">
			{#if type === 'delete'}
				<Icon icon="mdi:delete" class="mr-1 h-4 w-4" />
			{:else if type === 'edit'}
				<Icon icon="mdi:pencil" class="mr-1 h-4 w-4" />
			{/if}
			<span>{type === 'delete' ? 'Delete' : 'Edit'}</span>
		</div>
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New</span>
			</div>
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
		{networkInterfaces}
		{networkSwitches}
		{dhcpConfig}
		selectedRange={null}
		{dhcpRanges}
	/>
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		bind:open={modals.edit.open}
		bind:reload
		{networkInterfaces}
		{networkSwitches}
		{dhcpConfig}
		{selectedRange}
		{dhcpRanges}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'DHCP Range', element: activeRow?.id.toString() || '' }}
	customTitle="This action cannot be undone. This will permanently delete this DHCP range including all leases associated with it"
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
