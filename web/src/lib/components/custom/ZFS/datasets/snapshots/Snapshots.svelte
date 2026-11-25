<script lang="ts">
	import { getBasicSettings } from '$lib/api/system/settings';
	import { getPeriodicSnapshots } from '$lib/api/zfs/datasets';
	import TreeTableRemote from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateDetailed from '$lib/components/custom/ZFS/datasets/snapshots/CreateDetailed.svelte';
	import DeleteSnapshots from '$lib/components/zen/Storage/Snapshots/Delete.svelte';
	import Jobs from '$lib/components/custom/ZFS/datasets/snapshots/Jobs.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { BasicSettings } from '$lib/types/system/settings';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import {
		GZFSDatasetTypeSchema,
		type PeriodicSnapshot,
		type SnapshotSummary
	} from '$lib/types/zfs/dataset';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { IsDocumentVisible, resource, useInterval, watch, PersistedState } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon, sizeFormatter, deselectAllRows, getTable } from '$lib/utils/table';
	import { plural } from '$lib/utils';
	import { convertDbTime } from '$lib/utils/time';
	import {
		snapshotRowCreation,
		snapshotRowMarkers,
		snapshotRowSummary
	} from '$lib/utils/zfs/snapshot';
	import { storage } from '$lib';

	interface Data {
		hostname: string;
		basicSettings: BasicSettings;
		periodicSnapshots: PeriodicSnapshot[];
	}

	let visible = new IsDocumentVisible();
	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const basicSettings = resource(
		() => 'basic-settings',
		async (_key, _previousKey, { data: previousSettings }): Promise<BasicSettings> => {
			const results = await getBasicSettings();
			if (isAPIResponse(results)) {
				return previousSettings ?? data.basicSettings;
			}
			updateCache('basic-settings', results);
			return results;
		},
		{
			initialValue: data.basicSettings
		}
	);

	// svelte-ignore state_referenced_locally
	const periodicSnapshots = resource(
		() => 'zfs-periodic-snapshots',
		async () => {
			const results = await getPeriodicSnapshots();
			updateCache('zfs-periodic-snapshots', results);
			return results;
		},
		{
			initialValue: data.periodicSnapshots
		}
	);

	let snapshotsTable = $state<ReturnType<typeof TreeTableRemote>>();

	useInterval(30_000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				periodicSnapshots.refetch();
				const dialogOpen =
					deleteTargets !== null || modals.snapshot.create.open || modals.snapshot.periodics.open;
				if (!dialogOpen && !reload) void snapshotsTable?.refreshQuietly();
			}
		}
	});

	let pools = $derived.by(() => {
		if (
			basicSettings.current &&
			!isAPIResponse(basicSettings.current) &&
			basicSettings.current.pools
		) {
			return basicSettings.current.pools;
		}
		return [];
	});

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
	let deleteTargets = $state<SnapshotSummary[] | null>(null);

	function closeDelete() {
		deleteTargets = null;
		activeRows = null;
		deselectAllRows(getTable('snapshots-datasets-tt'));
	}

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
					const name = renderWithIcon('carbon:ibm-cloud-vpc-block-storage-snapshots', value);
					const markers = snapshotRowMarkers(cell.getRow().getData());
					return markers
						? `<span class="inline-flex items-center gap-1.5">${name}${markers}</span>`
						: name;
				}
			},
			{
				field: 'creation',
				title: 'Created',
				mutator: (_value: unknown, data: Record<string, unknown>) => snapshotRowCreation(data),
				formatter: (cell: CellComponent) => {
					const seconds = cell.getValue();
					return seconds ? convertDbTime(new Date(seconds * 1000).toISOString()) : '-';
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
	let nameFilterOpen = $state(false);

	const nameFilterDefaultValues = ['bk_j', 'svms_', 'sjs_', '_template', 'ha_'];

	function createNameFilterData() {
		const defaults = [
			{ value: 'bk_j', label: 'Backups' },
			{ value: 'svms_', label: 'VM Snapshots' },
			{ value: 'sjs_', label: 'Jail Snapshots' },
			{ value: '_template', label: 'Templates' },
			{ value: 'ha_', label: 'HA Snapshots' }
		];
		const defaultValues = new Set(nameFilterDefaultValues);
		const customEntries = persistedNameFilter.current
			.filter((value) => !defaultValues.has(value))
			.map((value) => ({ value, label: value }));

		return [...defaults, ...customEntries];
	}

	const persistedNameFilter = new PersistedState<string[]>('snapshots-name-filter', [
		...nameFilterDefaultValues
	]);
	const legacyNameFilterDefaults = nameFilterDefaultValues.slice(0, -1);

	if (
		legacyNameFilterDefaults.every((value) => persistedNameFilter.current.includes(value)) &&
		!persistedNameFilter.current.includes('ha_')
	) {
		persistedNameFilter.current = [...persistedNameFilter.current, 'ha_'];
	}

	let nameFilterData = $state(createNameFilterData());

	let nameFilter = $state<string[]>(
		persistedNameFilter.current.length > 0
			? persistedNameFilter.current
			: [...nameFilterDefaultValues]
	);

	watch(
		() => nameFilter,
		(value) => {
			persistedNameFilter.current = value;
			reload = true;
		}
	);

	let extraParams = $derived({
		datasetType: GZFSDatasetTypeSchema.enum.SNAPSHOT,
		...(nameFilter.length > 0 ? { nameFilter: nameFilter.join(',') } : {})
	});

	let modals = $state({
		snapshot: {
			create: {
				open: false
			},
			periodics: {
				open: false,
				pool: ''
			}
		}
	});

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
				deleteTargets = (activeRows ?? []).map((row) => snapshotRowSummary(row));
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
					{#each pools as pool (pool)}
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

		<div class="ml-auto flex items-center gap-1">
			<Combobox
				bind:open={nameFilterOpen}
				bind:value={nameFilter}
				bind:data={nameFilterData}
				placeholder="Filter"
				multiple={true}
				showSelected={false}
				showSelectedCountLabel=" Filtered"
				initialValues={[...nameFilterDefaultValues]}
				triggerWidth="w-36"
				buttonClass="h-6.5"
				width="w-52"
				classes=""
			/>
			{@render button('view-periodics')}
			{@render button('reload')}
		</div>
	</div>

	<TreeTableRemote
		bind:this={snapshotsTable}
		name="snapshots-datasets-tt"
		index="name"
		data={table}
		bind:query
		bind:reload
		ajaxURL="/api/zfs/datasets/paginated"
		bind:parentActiveRow={activeRows}
		{extraParams}
		dataTree={false}
		initialSort={[{ column: 'creation', dir: 'desc' }]}
	/>
</div>

<!-- Create Snapshot -->
{#if modals.snapshot.create.open && basicSettings.current && !isAPIResponse(basicSettings.current)}
	<CreateDetailed
		bind:open={modals.snapshot.create.open}
		bind:reload
		basicSettings={basicSettings.current}
	/>
{/if}

{#if deleteTargets && deleteTargets.length > 0}
	<DeleteSnapshots
		hostname={data.hostname}
		targets={deleteTargets}
		protectSylve={false}
		onClose={closeDelete}
		onDone={() => (reload = true)}
	/>
{/if}

{#if modals.snapshot.periodics.open && activePeriodics && activePeriodics.length > 0}
	<Jobs
		bind:open={modals.snapshot.periodics.open}
		periodicSnapshots={activePeriodics}
		bind:reload
	/>
{/if}
