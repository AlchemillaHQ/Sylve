<script lang="ts">
	import { getJailById, updateResourceLimits } from '$lib/api/jail/jail';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CPU from '$lib/components/custom/Jail/Hardware/CPU.svelte';
	import RAM from '$lib/components/custom/Jail/Hardware/RAM.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable } from '$lib/utils/numbers';
	import { generateNanoId } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { resource } from 'runed';
	import { untrack } from 'svelte';
	import { renderWithIcon } from '$lib/utils/table';
	import type { CPUInfo } from '$lib/types/info/cpu';

	interface Data {
		jail: Jail;
		ram: RAMInfo;
		cpu: CPUInfo;
	}

	let { data }: { data: Data } = $props();
	let reload = $state(true);

	const jail = resource(
		() => 'jail-' + data.jail.ctId,
		async (key) => {
			const jail = await getJailById(data.jail.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			lazy: true,
			initialValue: data.jail
		}
	);

	let options = {
		ram: {
			value: data.jail.memory,
			open: false
		},
		cpu: {
			value: data.jail.cores,
			open: false
		},
		resourceLimits: {
			open: false
		}
	};

	let properties = $state(options);

	$effect(() => {
		if (reload) {
			untrack(() => {
				jail.refetch().then(() => {
					properties.ram.value = jail.current.memory;
					properties.cpu.value = jail.current.cores;
					reload = false;
				});
			});
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
				formatter: function (cell, formatterParams, onRendered) {
					const value = cell.getValue();
					if (value === 'Unlimited') {
						return renderWithIcon('mdi:infinity', '');
					}
					return value;
				}
			}
		] as Column[],
		rows: [
			{
				id: generateNanoId(`${properties.ram.value}-ram`),
				property: 'RAM',
				value: properties.ram.value ? bytesToHumanReadable(properties.ram.value) : 'Unlimited'
			},
			{
				id: generateNanoId(`${properties.cpu.value}-cpu`),
				property: 'CPU',
				value: properties.cpu.value ? properties.cpu.value : 'Unlimited'
			}
		]
	});
</script>

{#snippet button(property: 'ram' | 'cpu' | 'resource-limits', title: string)}
	{#if property === 'resource-limits'}
		{#if !activeRows || activeRows.length === 0}
			<Button
				onclick={() => {
					properties.resourceLimits.open = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					{#if jail.current.resourceLimits}
						<span class="icon-[lsicon--disable-filled] mr-1 h-4 w-4"></span>
						<span>Disable Resource Limits</span>
					{:else}
						<span class="icon-[clarity--resource-pool-line] mr-1 h-4 w-4"></span>
						<span>Enable Resource Limits</span>
					{/if}
				</div>
			</Button>
		{/if}
	{:else}
		<Button
			onclick={() => {
				properties[property].open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5 disabled:pointer-events-auto!"
			title={!jail.current.resourceLimits ? 'Enable resource limits to edit' : ''}
			disabled={!jail.current.resourceLimits}
		>
			<div class="flex items-center">
				<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

				<span>Edit {title}</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		{@render button('resource-limits', 'Resource Limits')}

		{#if activeRows && activeRows?.length !== 0}
			{#if activeRow && activeRow.property === 'RAM'}
				{@render button('ram', 'RAM')}
			{/if}

			{#if activeRow && activeRow.property === 'CPU'}
				{@render button('cpu', 'CPU')}
			{/if}
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'jail-hardware-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.ram.open}
	<RAM bind:open={properties.ram.open} ram={data.ram} jail={jail.current} bind:reload />
{/if}

{#if properties.cpu.open}
	<CPU bind:open={properties.cpu.open} cpu={data.cpu} jail={jail.current} bind:reload />
{/if}

<AlertDialog
	open={properties.resourceLimits.open}
	customTitle={jail.current.resourceLimits
		? 'This will give unlimited resources to this jail, proceed with <b>caution!</b>'
		: 'This will enable resource limits for this jail, defaulting to <b>1 GB RAM</b> and <b>1 vCPU</b>, you can change this later'}
	actions={{
		onConfirm: async () => {
			const response = await updateResourceLimits(jail.current.ctId, !jail.current.resourceLimits);
			reload = true;
			if (response.error) {
				handleAPIError(response);
				let adjective = jail.current.resourceLimits ? 'disable' : 'enable';
				toast.error(`Failed to ${adjective} resource limits`, {
					position: 'bottom-center'
				});

				return;
			}

			let adjective = jail.current.resourceLimits ? 'disabled' : 'enabled';
			toast.success(`Resource limits ${adjective}`, {
				position: 'bottom-center'
			});
			properties.resourceLimits.open = false;
		},
		onCancel: () => {
			properties.resourceLimits.open = false;
		}
	}}
></AlertDialog>
