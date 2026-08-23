<script lang="ts">
	import { getVmByIdResult } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import BootRom from '$lib/components/custom/VM/Options/BootRom.svelte';
	import Clock from '$lib/components/custom/VM/Options/Clock.svelte';
	import CloudInit from '$lib/components/custom/VM/Options/CloudInit.svelte';
	import ExtraBhyveOptions from '$lib/components/custom/VM/Options/ExtraBhyveOptions.svelte';
	import IgnoreUMSR from '$lib/components/custom/VM/Options/IgnoreUMSR.svelte';
	import QemuGuestAgent from '$lib/components/custom/VM/Options/QemuGuestAgent.svelte';
	import ShutdownWaitTime from '$lib/components/custom/VM/Options/ShutdownWaitTime.svelte';
	import StartOrder from '$lib/components/custom/VM/Options/StartOrder.svelte';
	import WoL from '$lib/components/custom/VM/Options/WoL.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Architecture } from '$lib/types/info/cpu';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { isBoolean } from '$lib/utils/string';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, watch } from 'runed';
	import { getContext, onMount, untrack } from 'svelte';

	interface Data {
		node: string;
		rid: number;
		vm: VM;
		architecture: Architecture;
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');
	const vmIdentity = (node: string, rid: number) => `${node}\u0000${rid}`;
	const lastVMByIdentity: Record<string, VM> = Object.create(null);
	lastVMByIdentity[vmIdentity(initialData.node, initialData.rid)] = initialData.vm;

	const vm = resource(
		() => [data.node, data.rid] as const,
		async ([node, rid], _, { signal }) => {
			const result = await getVmByIdResult(rid, { hostname: node, signal });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVMByIdentity[vmIdentity(node, rid)] ?? data.vm;
			}
			lastVMByIdentity[vmIdentity(node, rid)] = result;
			await updateCache(`vm-${rid}`, result, node);
			return result;
		},
		{
			initialValue: initialData.vm
		}
	);

	let reload = $state(false);
	watch(
		() => reload,
		(shouldReload) => {
			if (shouldReload) {
				reload = false;
				vm.refetch();
			}
		}
	);

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let isLifecycleActive = $derived(!!domain.current?.pendingAction);
	let isDomainShutoff = $derived(
		!isLifecycleActive &&
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === 'shutoff'
	);

	let activeRows: Row[] = $state([]);
	let activeRow: Row | null = $derived(activeRows[0] ?? null);
	let query = $state('');

	function bootROMLabel(bootROM: VM['bootRom']): string {
		switch (bootROM) {
			case 'none':
				return 'None';
			case 'uboot':
				return 'U-Boot (Default)';
			default:
				return 'UEFI (Default)';
		}
	}

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (isBoolean(value)) {
						if (value === true || value === 'true') return 'Yes';
						if (value === false || value === 'false') return 'No';
					}
					return value;
				}
			}
		],
		rows: [
			{
				id: 'vm-option-start-order',
				property: 'Start At Boot / Start Order',
				value: `${vm.current.startAtBoot ? 'Yes' : 'No'} / ${vm.current.startOrder}`
			},
			{
				id: 'vm-option-wol',
				property: 'Wake on LAN',
				value: vm.current.wol
			},
			{
				id: 'vm-option-time-offset',
				property: 'Clock Offset',
				value: vm.current.timeOffset === 'utc' ? 'UTC' : 'Local Time'
			},
			{
				id: 'vm-option-boot-rom',
				property: 'Boot ROM',
				value: bootROMLabel(vm.current.bootRom)
			},
			{
				id: 'vm-option-shutdown-wait-time',
				property: 'Shutdown Wait Time',
				value: `${vm.current.shutdownWaitTime} seconds`
			},
			{
				id: 'vm-option-cloud-init',
				property: 'Cloud Init',
				value:
					vm.current.cloudInitData ||
					vm.current.cloudInitMetaData ||
					vm.current.cloudInitNetworkConfig
						? 'Configured'
						: 'Not Configured'
			},
			{
				id: 'vm-option-extra-bhyve-options',
				property: 'Extra Bhyve Options',
				value:
					vm.current.extraBhyveOptions && vm.current.extraBhyveOptions.length > 0
						? `${vm.current.extraBhyveOptions.length} configured`
						: 'Not Configured'
			},
			{
				id: 'vm-option-ignore-umsrs',
				property: 'Ignore Unimplemented MSRs Accesses',
				value: vm.current.ignoreUMSR
			},
			{
				id: 'vm-option-qemu-guest-agent',
				property: 'QEMU Guest Agent',
				value: vm.current.qemuGuestAgent
			}
		]
	});

	let properties = $state({
		startOrder: { open: false },
		wol: { open: false },
		timeOffset: { open: false },
		bootRom: { open: false },
		shutdownWaitTime: { open: false },
		cloudInit: { open: false },
		extraBhyveOptions: { open: false },
		ignoreUMSR: { open: false },
		qemuGuestAgent: { open: false }
	});
</script>

{#snippet button(
	type:
		| 'startOrder'
		| 'wol'
		| 'timeOffset'
		| 'bootRom'
		| 'shutdownWaitTime'
		| 'cloudInit'
		| 'extraBhyveOptions'
		| 'ignoreUMSR'
		| 'qemuGuestAgent',
	title: string,
	requireShutoff: boolean = true
)}
	<Button
		onclick={() => {
			properties[type].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
		title={requireShutoff && !isDomainShutoff
			? `${title} can only be edited when the VM is shut off`
			: ''}
		disabled={requireShutoff ? !isDomainShutoff : false}
	>
		<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit {title}" />
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRow}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if activeRow.property === 'Start At Boot / Start Order'}
				{@render button('startOrder', 'Start At Boot / Start Order', false)}
			{:else if activeRow.property === 'Wake on LAN'}
				{@render button('wol', 'Wake on LAN', false)}
			{:else if activeRow.property === 'Clock Offset'}
				{@render button('timeOffset', 'Clock Offset')}
			{:else if activeRow.property === 'Boot ROM'}
				{@render button('bootRom', 'Boot ROM')}
			{:else if activeRow.property === 'Shutdown Wait Time'}
				{@render button('shutdownWaitTime', 'Shutdown Wait Time', false)}
			{:else if activeRow.property === 'Cloud Init'}
				{@render button('cloudInit', 'Cloud Init')}
			{:else if activeRow.property === 'Extra Bhyve Options'}
				{@render button('extraBhyveOptions', 'Extra Bhyve Options')}
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
			name="vm-options-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.wol.open}
	<WoL bind:open={properties.wol.open} node={data.node} vm={vm.current} bind:reload />
{/if}

{#if properties.startOrder.open}
	<StartOrder bind:open={properties.startOrder.open} node={data.node} vm={vm.current} bind:reload />
{/if}

{#if properties.timeOffset.open}
	<Clock bind:open={properties.timeOffset.open} node={data.node} vm={vm.current} bind:reload />
{/if}

{#if properties.bootRom.open}
	<BootRom
		bind:open={properties.bootRom.open}
		node={data.node}
		architecture={data.architecture}
		vm={vm.current}
		bind:reload
	/>
{/if}

{#if properties.shutdownWaitTime.open}
	<ShutdownWaitTime
		bind:open={properties.shutdownWaitTime.open}
		node={data.node}
		vm={vm.current}
		bind:reload
	/>
{/if}

{#if properties.cloudInit.open}
	<CloudInit bind:open={properties.cloudInit.open} node={data.node} vm={vm.current} bind:reload />
{/if}

{#if properties.extraBhyveOptions.open}
	<ExtraBhyveOptions
		bind:open={properties.extraBhyveOptions.open}
		node={data.node}
		vm={vm.current}
		bind:reload
	/>
{/if}

{#if properties.ignoreUMSR.open}
	<IgnoreUMSR bind:open={properties.ignoreUMSR.open} node={data.node} vm={vm.current} bind:reload />
{/if}

{#if properties.qemuGuestAgent.open}
	<QemuGuestAgent
		bind:open={properties.qemuGuestAgent.open}
		node={data.node}
		vm={vm.current}
		bind:reload
	/>
{/if}
