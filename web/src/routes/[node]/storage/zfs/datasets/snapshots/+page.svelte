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
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import {
		GZFSDatasetTypeSchema,
		type Dataset,
		type PeriodicSnapshot
	} from '$lib/types/zfs/dataset';
	import { updateCache } from '$lib/utils/http';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon, sizeFormatter } from '$lib/utils/table';
	import { plural } from '$lib/utils';
	import { storage } from '$lib';

	interface Data {
		basicSettings: BasicSettings;
		periodicSnapshots: PeriodicSnapshot[];
		snapshots: Dataset[];
	}

	let visible = new IsDocumentVisible();
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

	useInterval(1000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				periodicSnapshots.refetch();
			}
		}
	});

	let pools = $derived(basicSettings.current.pools || []);
	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (!value) return;

			const MAX_RETRIES = 3;
			const DELAY = 500;

			(async () => {
				for (let i = 0; i < MAX_RETRIES && reload; i++) {
					await basicSettings.refetch();
					await periodicSnapshots.refetch();
					await new Promise((r) => setTimeout(r, DELAY));
				}

				reload = false;
			})();
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
			periodics: {
				open: false,
				pool: ''
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

	let activePeriodics = $derived.by(() => {
		return modals.snapshot.periodics.open && modals.snapshot.periodics.pool
			? periodicSnapshots.current.filter((p) => p.pool === modals.snapshot.periodics.pool)
			: [];
	});
</script>

{#snippet button(type: string)}
	{#if type === 'delete-snapshot' && activeRows && activeRows.length >= 1}
		<Button
			onclick={() => {
				modals.snapshot.delete.open = true;
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

	{#if type === 'view-periodics' && periodicSnapshots.current.length > 0}
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				<Button size="sm" class="h-6 mt-0.5" variant="outline">
					<div class="flex items-center">
						<span class="icon-[mdi--clock] h-4 w-4"></span>
					</div>
				</Button>
			</DropdownMenu.Trigger>
			<DropdownMenu.Content>
				<DropdownMenu.Group>
					<DropdownMenu.Label>View Periodics</DropdownMenu.Label>
					<DropdownMenu.Separator />
					{#each pools as pool}
						{#if periodicSnapshots.current.find((p) => p.pool === pool)}
							<DropdownMenu.Item
								onclick={() => {
									modals.snapshot.periodics.open = true;
									modals.snapshot.periodics.pool = pool;
								}}
							>
								{pool}
							</DropdownMenu.Item>
						{/if}
					{/each}
				</DropdownMenu.Group>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	{/if}
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

		<div class="ml-auto">
			{@render button('view-periodics')}
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

{#if modals.snapshot.periodics.open && activePeriodics && activePeriodics.length > 0}
	<Jobs
		bind:open={modals.snapshot.periodics.open}
		periodicSnapshots={activePeriodics}
		bind:reload
	/>
{/if}
