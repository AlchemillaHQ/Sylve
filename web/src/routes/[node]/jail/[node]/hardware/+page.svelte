<script lang="ts">
	import { getJails, updateResourceLimits } from '$lib/api/jail/jail';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CPU from '$lib/components/custom/Jail/Hardware/CPU.svelte';
	import RAM from '$lib/components/custom/Jail/Hardware/RAM.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable } from '$lib/utils/numbers';
	import { generateNanoId } from '$lib/utils/string';
	import { useQueries } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		jail: Jail;
		jails: Jail[];
		ram: RAMInfo;
	}

	let { data }: { data: Data } = $props();
	const results = useQueries([
		{
			queryKey: ['jail-list'],
			queryFn: async () => {
				return await getJails();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.jails,
			onSuccess: (data: Jail[]) => {
				updateCache('jail-list', data);
			}
		}
	]);

	let jails = $derived($results[0].data);
	let jail = $derived.by(() => {
		if (jails) {
			const found = jails.find((j) => j.ctId === data.jail?.ctId);
			return found || data.jail;
		}

		return data.jail;
	});

	let options = {
		ram: {
			value: jail?.memory,
			open: false
		},
		cpu: {
			value: jail?.cores,
			open: false
		},
		resourceLimits: {
			open: false
		}
	};

	let properties = $state(options);

	$effect(() => {
		if (jail) {
			properties.ram.value = jail.memory;
			properties.cpu.value = jail.cores;
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
				field: 'value'
			}
		],
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
		<Button
			onclick={() => {
				properties.resourceLimits.open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
		>
			<div class="flex items-center">
				{#if jail.resourceLimits}
					<span class="icon-[lsicon--disable-filled] mr-1 h-4 w-4"></span>
					<span>Disable Resource Limits</span>
				{:else}
					<span class="icon-[clarity--resource-pool-line] mr-1 h-4 w-4"></span>
					<span>Enable Resource Limits</span>
				{/if}
			</div>
		</Button>
	{:else}
		<Button
			onclick={() => {
				properties[property].open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5 disabled:!pointer-events-auto"
			title={!jail.resourceLimits ? 'Enable resource limits to edit' : ''}
			disabled={!jail.resourceLimits}
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
	<RAM bind:open={properties.ram.open} ram={data.ram} {jail} />
{/if}

{#if properties.cpu.open}
	<CPU bind:open={properties.cpu.open} {jail} />
{/if}

<AlertDialog
	open={properties.resourceLimits.open}
	customTitle={jail.resourceLimits
		? 'This will give unlimited resources to this jail, proceed with <b>caution!</b>'
		: 'This will enable resource limits for this jail, defaulting to <b>1 GB RAM</b> and <b>1 vCPU</b>, you can change this later'}
	actions={{
		onConfirm: async () => {
			const response = await updateResourceLimits(jail.ctId, !jail.resourceLimits);
			if (response.error) {
				handleAPIError(response);
				let adjective = jail.resourceLimits ? 'disable' : 'enable';
				toast.error(`Failed to ${adjective} resource limits`, {
					position: 'bottom-center'
				});

				return;
			}

			let adjective = jail.resourceLimits ? 'disabled' : 'enabled';
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
