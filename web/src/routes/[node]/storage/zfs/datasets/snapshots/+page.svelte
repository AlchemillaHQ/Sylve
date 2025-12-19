<script lang="ts">
	import { getBasicSettings } from '$lib/api/system/settings';
	import { getPeriodicSnapshots } from '$lib/api/zfs/datasets';
	import TreeTableRemote from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateDetailed from '$lib/components/custom/ZFS/datasets/snapshots/CreateDetailed.svelte';
	import DeleteSnapshot from '$lib/components/custom/ZFS/datasets/snapshots/Delete.svelte';
	import Jobs from '$lib/components/custom/ZFS/datasets/snapshots/Jobs.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { BasicSettings } from '$lib/types/system/settings';
	import {
		GZFSDatasetTypeSchema,
		type Dataset,
		type PeriodicSnapshot
	} from '$lib/types/zfs/dataset';
	import { updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon, sizeFormatter } from '$lib/utils/table';
	import { plural } from '$lib/utils';

	interface Data {
		basicSettings: BasicSettings;
		periodicSnapshots: PeriodicSnapshot[];
		snapshots: Dataset[];
	}

	let { data }: { data: Data } = $props();

	const basicSettings = resource(
		() => 'basic-settings',
		async (key, prevKey, { signal }) => {
			const results = await getBasicSettings();
			updateCache('basic-settings', results);
			return results;
		},
		{
			initialValue: data.basicSettings
		}
	);

	const periodicSnapshots = resource(
		() => 'zfs-periodic-snapshots',
		async (key, prevKey, { signal }) => {
			const results = await getPeriodicSnapshots();
			updateCache('zfs-periodic-snapshots', results);
			return results;
		},
		{
			initialValue: data.periodicSnapshots
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				basicSettings.refetch();
				periodicSnapshots.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeDatasets: Dataset[] | null = $derived.by(() => {
		let snapshots: Dataset[] = [];

		if (activeRows && Array.isArray(activeRows) && activeRows.length >= 1) {
			for (const row of activeRows) {
				const snapshot = {
					name: row.name as string,
					pool: row.pool as string,
					guid: row.guid as string,
					type: GZFSDatasetTypeSchema.enum.SNAPSHOT,
					used: row.used as number,
					referenced: row.referenced as number,
					available: 0,
					mountpoint: ''
				};

				snapshots.push(snapshot);
			}
		}

		return snapshots;
	});

	let table = $derived({
		columns: [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'name',
				title: 'Name',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return renderWithIcon('carbon:ibm-cloud-vpc-block-storage-snapshots', value);
				}
			},
			{
				field: 'used',
				title: 'Used',
				formatter: sizeFormatter
			},
			{
				field: 'referenced',
				title: 'Referenced',
				formatter: sizeFormatter
			},
			{
				field: 'pool',
				title: 'Pool',
				visible: false,
				formatter: (cell: CellComponent) => {
					return cell.getValue();
				}
			}
		] as Column[],
		rows: []
	});

	let query = $state('');
	let modals = $state({
		snapshot: {
			create: {
				open: false
			},
			delete: {
				open: false
			},
			bulkDelete: {
				open: false
			},
			periodics: {
				open: false
			}
		}
	});

	watch(
		() => modals.snapshot.delete.open,
		(value) => {
			if (!value) {
				activeRows = null;
			}
		}
	);
</script>

{#snippet button(type: string)}
	{#if type === 'delete-snapshot' && activeRows && activeRows.length >= 1}
		<Button
			onclick={() => {
				console.log('activeRows', activeRows);
				if (activeRows?.length === 1) {
					modals.snapshot.delete.open = true;
					modals.snapshot.bulkDelete.open = false;
				} else {
					modals.snapshot.bulkDelete.open = true;
					modals.snapshot.delete.open = false;
				}
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
				{plural(activeRows?.length, ['Delete Snapshot', 'Delete # Snapshots'])}
			</div>
		</Button>
	{/if}

	{#if type === 'reload'}
		<Button
			onclick={() => {
				reload = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--reload] h-4 w-4"></span>
			</div>
		</Button>
	{/if}

	<!-- {#if type === 'view-periodics' && activePool && activePeriodics && activePeriodics.length > 0}
		<Button
			onclick={() => {
				modals.snapshot.periodics.open = true;
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:pointer-events-auto! disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--clock-time-four] mr-1 h-4 w-4"></span>
				<span>View Periodics</span>
			</div>
		</Button>
	{/if} -->
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				modals.snapshot.create.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('delete-snapshot')}
		{@render button('view-periodics')}

		<div class="ml-auto">
			{@render button('reload')}
		</div>
	</div>

	<TreeTableRemote
		name={'snapshots-datasets-tt'}
		data={table}
		bind:query
		bind:reload
		ajaxURL="/api/zfs/datasets/paginated"
		bind:parentActiveRow={activeRows}
		extraParams={{ datasetType: GZFSDatasetTypeSchema.enum.SNAPSHOT }}
	/>
</div>

<!-- Create Snapshot -->
{#if modals.snapshot.create.open}
	<CreateDetailed bind:open={modals.snapshot.create.open} bind:reload />
{/if}

{#if modals.snapshot.delete.open && activeDatasets && activeDatasets.length >= 1}
	<DeleteSnapshot
		bind:open={modals.snapshot.delete.open}
		datasets={activeDatasets}
		askRecursive={false}
		bind:reload
	/>
{/if}

<!-- {#if modals.snapshot.periodics.open && activePeriodics && activePeriodics.length > 0}
	<Jobs
		bind:open={modals.snapshot.periodics.open}
		{pools}
		{datasets}
		periodicSnapshots={activePeriodics}
		bind:reload
	/>
{/if} -->
