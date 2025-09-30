<script lang="ts">
	import { getVMs } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Clock from '$lib/components/custom/VM/Options/Clock.svelte';
	import Serial from '$lib/components/custom/VM/Options/Serial.svelte';
	import StartOrder from '$lib/components/custom/VM/Options/StartOrder.svelte';
	import WoL from '$lib/components/custom/VM/Options/WoL.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { generateNanoId, isBoolean } from '$lib/utils/string';
	import Icon from '@iconify/svelte';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		vm: VM;
		vms: VM[];
		domain: VMDomain;
	}

	let { data }: { data: Data } = $props();
	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'vm-list',
			queryFn: async () => {
				return await getVMs();
			},
			keepPreviousData: true,
			initialData: data.vms,
			onSuccess: (data: VM[]) => {
				updateCache('vm-list', data);
			}
		}
	]);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('vm-list');
			reload = false;
		}
	});

	let vms: VM[] = $derived($results[0].data ? $results[0].data : data.vms);
	let vm: VM | null = $derived(
		vms && data.vm ? (vms.find((v: VM) => v.vmId === data.vm.vmId) ?? null) : null
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (isBoolean(value)) {
						if (value === true || value === 'true') {
							return 'Yes';
						} else if (value === false || value === 'false') {
							return 'No';
						}
					}

					return value;
				}
			}
		],
		rows: [
			{
				id: generateNanoId('startOrder'),
				property: 'Start At Boot / Start Order',
				value: `${vm?.startAtBoot ? 'Yes' : 'No'} / ${vm?.startOrder}`
			},
			{
				id: generateNanoId('wol'),
				property: 'Wake on LAN',
				value: vm?.wol
			},
			{
				id: generateNanoId('timeOffset'),
				property: 'Clock Offset',
				value: vm ? (vm.timeOffset === 'utc' ? 'UTC' : 'Local Time') : 'N/A'
			},
			{
				id: generateNanoId('serial'),
				property: 'Serial Console',
				value: vm?.serial ? 'Enabled' : 'Disabled'
			}
		]
	});

	let properties = $state({
		startOrder: { open: false },
		wol: { open: false },
		timeOffset: { open: false },
		serial: { open: false }
	});
</script>

{#snippet button(type: 'startOrder' | 'wol' | 'timeOffset' | 'serial', title: string)}
	<Button
		onclick={() => {
			properties[type].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
		title={data.domain.status === 'Shutoff'
			? ''
			: `${title} can only be edited when the VM is shut off`}
		disabled={data.domain.status ? data.domain.status !== 'Shutoff' : false}
	>
		<div class="flex items-center">
			<Icon icon="mdi:pencil" class="mr-1 h-4 w-4" />
			<span>Edit {title}</span>
		</div>
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRows && activeRows?.length !== 0}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if activeRow.property === 'Start At Boot / Start Order'}
				{@render button('startOrder', 'Start At Boot / Start Order')}
			{:else if activeRow.property === 'Wake on LAN'}
				{@render button('wol', 'Wake on LAN')}
			{:else if activeRow.property === 'Clock Offset'}
				{@render button('timeOffset', 'Clock Offset')}
			{:else if activeRow.property === 'Serial Console'}
				{@render button('serial', 'Serial Console')}
			{/if}
		</div>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'vm-options-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.wol.open && vm}
	<WoL bind:open={properties.wol.open} {vm} bind:reload />
{/if}

{#if properties.startOrder.open && vm}
	<StartOrder bind:open={properties.startOrder.open} {vm} bind:reload />
{/if}

{#if properties.timeOffset.open && vm}
	<Clock bind:open={properties.timeOffset.open} {vm} bind:reload />
{/if}

{#if properties.serial.open && vm}
	<Serial bind:open={properties.serial.open} {vm} bind:reload />
{/if}
