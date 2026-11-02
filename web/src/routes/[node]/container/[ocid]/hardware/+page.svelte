<script lang="ts">
	import EditDialog from '$lib/components/custom/Container/EditDialog.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { containerBusyTitle, type ContainerContext } from '$lib/types/container/container';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import {
		formatBytesBinary,
		normalizeSizeInputExact,
		parseSizeInputToBytes
	} from '$lib/utils/bytes';
	import { renderWithIcon } from '$lib/utils/table';
	import { getContext } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { ram: RAMInfo | null; cpu: CPUInfo | null } } = $props();

	const GiB = 1024 * 1024 * 1024;
	const container = getContext<ContainerContext>('container');
	let current = $derived(container.current);
	let limited = $derived(current.cores > 0 || current.memory > 0);

	let activeRows = $state<Row[] | null>(null);
	let activeRow = $derived<Row | null>(activeRows?.[0] ?? null);
	let query = $state('');
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell) => {
					const value = cell.getValue();
					return value === 'Unlimited' ? renderWithIcon('mdi:infinity', '') : value;
				}
			}
		] as Column[],
		rows: [
			{
				id: 'ram',
				property: 'RAM',
				value: current.memory ? formatBytesBinary(current.memory) : 'Unlimited'
			},
			{ id: 'cpu', property: 'CPU', value: current.cores || 'Unlimited' }
		]
	});

	let dialog = $state({ open: false, cpu: false, cores: 1, ram: '' });
	let limitsDialog = $state(false);

	function edit(cpu: boolean) {
		dialog = {
			open: true,
			cpu,
			cores: current.cores || 1,
			ram: formatBytesBinary(current.memory || GiB)
		};
	}

	function fail(message: string) {
		toast.error(message, { position: 'bottom-center' });
		return false;
	}

	async function save() {
		if (dialog.cpu) {
			const cores = Number(dialog.cores);
			if (!Number.isInteger(cores) || cores < 1) return fail('CPU cores must be at least 1');
			if (data.cpu && cores > data.cpu.logicalCores) {
				return fail(`CPU cores larger than logical cores (${data.cpu.logicalCores})`);
			}
			return container.setOptions({ cores });
		}
		const memory = parseSizeInputToBytes(dialog.ram);
		if (memory === null) return fail('Invalid RAM value');
		if (memory < 16 * 1024 * 1024) return fail('RAM value must be at least 16 MiB');
		if (data.ram && memory > data.ram.total - GiB) {
			return fail('RAM value is too high, at least 1 GiB must be reserved for the host');
		}
		return container.setOptions({ memory });
	}

	async function toggleLimits() {
		const limits = limited ? { cores: 0, memory: 0 } : { cores: 1, memory: GiB };
		if (await container.setOptions(limits)) limitsDialog = false;
	}
</script>

{#snippet editButton(cpu: boolean)}
	{@const title = cpu ? 'CPU' : 'RAM'}
	<Button
		onclick={() => edit(cpu)}
		size="sm"
		variant="outline"
		class="h-6.5 disabled:pointer-events-auto!"
		title={!limited ? 'Enable resource limits to edit' : container.busy ? containerBusyTitle : ''}
		disabled={!limited || container.busy}
	>
		<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit {title}" />
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		{#if !activeRows || activeRows.length === 0}
			<Button
				onclick={() => (limitsDialog = true)}
				size="sm"
				variant="outline"
				class="h-6.5 disabled:pointer-events-auto!"
				disabled={container.busy}
				title={container.busy ? containerBusyTitle : ''}
			>
				{#if limited}
					<SpanWithIcon
						icon="icon-[lsicon--disable-filled]"
						size="h-4 w-4"
						gap="gap-1"
						title="Disable Resource Limits"
					/>
				{:else}
					<SpanWithIcon
						icon="icon-[clarity--resource-pool-line]"
						size="h-4 w-4"
						gap="gap-1"
						title="Enable Resource Limits"
					/>
				{/if}
			</Button>
		{/if}

		{#if activeRow?.id === 'ram'}
			{@render editButton(false)}
		{:else if activeRow?.id === 'cpu'}
			{@render editButton(true)}
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="container-hardware-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if dialog.open}
	<EditDialog
		bind:open={dialog.open}
		title={dialog.cpu ? 'CPU' : 'RAM'}
		icon={dialog.cpu ? 'icon-[solar--cpu-bold]' : 'icon-[ri--ram-fill]'}
		onSave={save}
	>
		{#if dialog.cpu}
			<CustomValueInput type="number" placeholder="1" bind:value={dialog.cores} />
		{:else}
			<CustomValueInput
				placeholder="1.0 GiB"
				bind:value={dialog.ram}
				onBlur={() => (dialog.ram = normalizeSizeInputExact(dialog.ram) ?? dialog.ram)}
			/>
		{/if}
	</EditDialog>
{/if}

<AlertDialog
	bind:open={limitsDialog}
	customTitle={limited
		? 'This will give unlimited resources to this container, proceed with <b>caution!</b>'
		: 'This will enable resource limits for this container, defaulting to <b>1 GiB RAM</b> and <b>1 vCPU</b>; you can change these later.'}
	keepOpenOnConfirm={true}
	actions={{ onConfirm: toggleLimits, onCancel: () => (limitsDialog = false) }}
/>
