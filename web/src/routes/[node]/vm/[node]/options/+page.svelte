<script lang="ts">
	import { getVmById } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Clock from '$lib/components/custom/VM/Options/Clock.svelte';
	import CloudInit from '$lib/components/custom/VM/Options/CloudInit.svelte';
	import IgnoreUMSR from '$lib/components/custom/VM/Options/IgnoreUMSR.svelte';
	import QemuGuestAgent from '$lib/components/custom/VM/Options/QemuGuestAgent.svelte';
	import ShutdownWaitTime from '$lib/components/custom/VM/Options/ShutdownWaitTime.svelte';
	import StartOrder from '$lib/components/custom/VM/Options/StartOrder.svelte';
	import WoL from '$lib/components/custom/VM/Options/WoL.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { generateNanoId, isBoolean } from '$lib/utils/string';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, useInterval, watch } from 'runed';
	import { storage } from '$lib';

	interface Data {
		vm: VM;
		domain: VMDomain;
	}

	let { data }: { data: Data } = $props();

	const vm = resource(
		() => `vm-${data.vm.rid}`,
		async (key) => {
			const result = await getVmById(data.vm.rid, 'rid');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.vm
		}
	);

	// let vm: VM | null = $derived(results.data);
	let reload = $state(false);

	useInterval(() => 1000, {
		callback: () => {
			if (storage.visible) {
				vm.refetch();
			}
		}
	});

	watch([() => storage.visible, () => reload], ([newVisible], [newReload]) => {
		if (newVisible || newReload) {
			vm.refetch();
		}
	});

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
				value: `${vm?.current.startAtBoot ? 'Yes' : 'No'} / ${vm?.current.startOrder || 0}`
			},
			{
				id: generateNanoId('wol'),
				property: 'Wake on LAN',
				value: vm?.current.wol || false
			},
			{
				id: generateNanoId('timeOffset'),
				property: 'Clock Offset',
				value: vm ? (vm.current.timeOffset === 'utc' ? 'UTC' : 'Local Time') : 'N/A'
			},
			{
				id: generateNanoId('shutdownWaitTime'),
				property: 'Shutdown Wait Time',
				value: vm ? `${vm.current.shutdownWaitTime} seconds` : 'N/A'
			},
			{
				id: generateNanoId('cloudInit'),
				property: 'Cloud Init',
				value:
					vm && (vm.current.cloudInitData || vm.current.cloudInitMetaData)
						? 'Configured'
						: 'Not Configured'
			},
			{
				id: generateNanoId('ignoreUMSRs'),
				property: 'Ignore Unimplemented MSRs Accesses',
				value: vm ? (vm.current.ignoreUMSR ? 'Yes' : 'No') : 'N/A'
			},
			{
				id: generateNanoId('qemuGuestAgent'),
				property: 'QEMU Guest Agent',
				value: vm ? (vm.current.qemuGuestAgent ? 'Yes' : 'No') : 'N/A'
			}
		]
	});

	let properties = $state({
		startOrder: { open: false },
		wol: { open: false },
		timeOffset: { open: false },
		shutdownWaitTime: { open: false },
		cloudInit: { open: false },
		ignoreUMSR: { open: false },
		qemuGuestAgent: { open: false }
	});
</script>

{#snippet button(
	type:
		| 'startOrder'
		| 'wol'
		| 'timeOffset'
		| 'shutdownWaitTime'
		| 'cloudInit'
		| 'ignoreUMSR'
		| 'qemuGuestAgent',
	title: string
)}
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
			<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
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
			{:else if activeRow.property === 'Shutdown Wait Time'}
				{@render button('shutdownWaitTime', 'Shutdown Wait Time')}
			{:else if activeRow.property === 'Cloud Init'}
				{@render button('cloudInit', 'Cloud Init')}
			{:else if activeRow.property === 'Ignore Unimplemented MSRs Accesses'}
				{@render button('ignoreUMSR', 'Ignore Unimplemented MSRs Accesses')}
			{:else if activeRow.property === 'QEMU Guest Agent'}
				{@render button('qemuGuestAgent', 'QEMU Guest Agent')}
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
	<WoL bind:open={properties.wol.open} vm={vm.current} bind:reload />
{/if}

{#if properties.startOrder.open && vm}
	<StartOrder bind:open={properties.startOrder.open} vm={vm.current} bind:reload />
{/if}

{#if properties.timeOffset.open && vm}
	<Clock bind:open={properties.timeOffset.open} vm={vm.current} bind:reload />
{/if}

{#if properties.shutdownWaitTime.open && vm}
	<ShutdownWaitTime bind:open={properties.shutdownWaitTime.open} vm={vm.current} bind:reload />
{/if}

{#if properties.cloudInit.open && vm}
	<CloudInit bind:open={properties.cloudInit.open} vm={vm.current} bind:reload />
{/if}

{#if properties.ignoreUMSR.open && vm}
	<IgnoreUMSR bind:open={properties.ignoreUMSR.open} vm={vm.current} bind:reload />
{/if}

{#if properties.qemuGuestAgent.open && vm}
	<QemuGuestAgent bind:open={properties.qemuGuestAgent.open} vm={vm.current} bind:reload />
{/if}
