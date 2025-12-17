<script lang="ts">
	import { storage } from '$lib';
	import { getPools } from '$lib/api/zfs/pool';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { type BasicSettings } from '$lib/types/system/settings';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import { untrack } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';
	import SingleValueDialog from '$lib/components/custom/Dialog/SingleValue.svelte';
	import { sameElements } from '$lib/utils/arr';
	import { toast } from 'svelte-sonner';
	import { getBasicSettings, updateUsablePools } from '$lib/api/system/settings';

	interface Data {
		pools: Zpool[];
		basicSettings: BasicSettings;
	}

	let { data }: { data: Data } = $props();

	const visible = new IsDocumentVisible();
	const pools = resource(
		() => 'zfs-pools-full',
		async (key, prevKey, { signal }) => {
			const results = await getPools(true);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: data.pools
		}
	);

	let poolOptions = $derived.by(() => {
		return pools.current.map((pool) => ({ label: pool.name, value: pool.name }));
	});

	const basicSettings = resource(
		() => 'system-basic-settings',
		async (key, prevKey, { signal }) => {
			const results = await getBasicSettings();
			updateCache(key, results);
			return results;
		},
		{
			initialValue: data.basicSettings
		}
	);

	useInterval(3000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				pools.refetch();
				basicSettings.refetch();
			}
		}
	});

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			untrack(() => {
				pools.refetch();
				basicSettings.refetch();
				reload = false;
			});
		}
	});

	let query: string = $state('');
	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{
				field: 'property',
				title: 'Property'
			},
			{
				field: 'value',
				title: 'Value',
				formatter: (cell: CellComponent) => {
					const row = cell.getRow();
					const property = row.getData().property;
					const value = cell.getValue();

					if (property === 'ZFS Pools' && Array.isArray(value)) {
						return value.join(', ');
					}

					return value;
				}
			}
		];

		const rows: Row[] = [
			{
				id: generateNanoId(`${JSON.stringify(basicSettings.current.pools)}`),
				property: 'ZFS Pools',
				value: basicSettings.current.pools
			}
		];

		return { columns, rows };
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : null);

	let modals = $state({
		zfsPools: {
			open: false,
			values: basicSettings.current.pools.join(',')
		}
	});

	async function save() {
		const toastOpts = {
			duration: 5000,
			position: 'bottom-center' as const
		};

		if (modals.zfsPools.open) {
			const newPools = modals.zfsPools.values
				.split(',')
				.map((p) => p.trim())
				.filter((p) => p.length > 0);

			if (newPools.length === 0) {
				toast.error('At least one ZFS Pool must be selected', toastOpts);
				return;
			}

			if (sameElements(newPools, basicSettings.current.pools)) {
				toast.info('No changes made to ZFS Pools', toastOpts);

				return;
			} else {
				const missingPools = basicSettings.current.pools.filter((pool) => !newPools.includes(pool));
				if (missingPools.length > 0) {
					toast.error('Cannot remove initialized ZFS Pools', toastOpts);
					return;
				}
			}

			const response = await updateUsablePools(newPools);
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to update ZFS Pools', toastOpts);
			} else {
				toast.success('ZFS Pools updated successfully', toastOpts);
				modals.zfsPools.open = false;
				modals.zfsPools.values = newPools.join(',');
			}
		}
	}
</script>

{#snippet button(property: 'ZFS Pools')}
	{#if activeRows?.length === 1}
		{#if property === 'ZFS Pools'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => {
					if (property === 'ZFS Pools') {
						modals.zfsPools.open = true;
					}
				}}
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit {property}</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		{@render button('ZFS Pools')}
	</div>

	<TreeTable
		name="system-basic-settings-tt"
		data={tableData}
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
	/>
</div>

<SingleValueDialog
	bind:open={modals.zfsPools.open}
	title="ZFS Pools"
	type="combobox"
	placeholder="Enter ZFS Pools"
	bind:value={modals.zfsPools.values}
	onSave={() => {
		save();
	}}
	options={poolOptions}
/>
