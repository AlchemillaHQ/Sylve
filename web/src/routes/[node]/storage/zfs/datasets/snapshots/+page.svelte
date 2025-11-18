<script lang="ts">
	import { bulkDelete, getDatasets, getPeriodicSnapshots } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialogModal from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateDetailed from '$lib/components/custom/ZFS/datasets/snapshots/CreateDetailed.svelte';
	import DeleteSnapshot from '$lib/components/custom/ZFS/datasets/snapshots/Delete.svelte';
	import Jobs from '$lib/components/custom/ZFS/datasets/snapshots/Jobs.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Dataset, GroupedByPool, PeriodicSnapshot } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { groupByPool } from '$lib/utils/zfs/dataset/dataset';
	import { generateTableData } from '$lib/utils/zfs/dataset/snapshot';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		pools: Zpool[];
		periodicSnapshots: PeriodicSnapshot[];
		datasets: Dataset[];
	}

	let { data }: { data: Data } = $props();
	let tableName = 'tt-zfsSnapshots';
	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'pools',
			queryFn: async () => {
				return await getPools();
			},
			keepPreviousData: false,
			initialData: data.pools,
			onSuccess: (data: Zpool[]) => {
				updateCache('pools', data);
			}
		},
		{
			queryKey: 'zfs-datasets',
			queryFn: async () => {
				return await getDatasets();
			},
			keepPreviousData: false,
			initialData: data.datasets,
			onSuccess: (data: Dataset[]) => {
				updateCache('zfs-datasets', data);
			}
		},
		{
			queryKey: 'periodic-snapshots',
			queryFn: async () => {
				return await getPeriodicSnapshots();
			},
			keepPreviousData: false,
			initialData: data.periodicSnapshots,
			onSuccess: (data: PeriodicSnapshot[]) => {
				updateCache('zfs-datasets', data);
			}
		}
	]);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('pools');
			queryClient.refetchQueries('zfs-datasets');
			queryClient.refetchQueries('periodic-snapshots');

			untrack(() => {
				reload = false;
			});
		}
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let grouped: GroupedByPool[] = $derived(groupByPool($results[0].data, $results[1].data));
	let pools: Zpool[] = $derived($results[0].data || []);
	let periodicSnapshots: PeriodicSnapshot[] = $derived($results[2].data || []);
	let datasets: Dataset[] = $derived.by(() => {
		return $results[1].data?.filter((dataset) => dataset.type !== 'snapshot') || [];
	});

	let tableData = $derived(generateTableData(grouped));
	let activePool: Zpool | null = $derived.by(() => {
		if (activeRow) {
			const poolGroup = grouped.find((pool) => pool.name === activeRow?.name);
			return poolGroup ? poolGroup.pool : null;
		}
		return null;
	});

	let activeDatasets: Dataset[] | null = $derived.by(() => {
		let snapshots: Dataset[] = [];

		if (activeRows) {
			for (const row of activeRows) {
				for (const group of grouped) {
					if (group.snapshots.length > 0) {
						const snapshot = group.snapshots.find((snapshot) => snapshot.name === row.name);
						if (snapshot) {
							snapshots.push(snapshot);
						}
					}
				}
			}
		}

		return snapshots;
	});

	let isPoolSelected: boolean = $derived.by(() => {
		if (activeRows) {
			return activeRows.some((row) => row.type === 'pool');
		}
		return false;
	});

	let activePeriodics: PeriodicSnapshot[] = $derived.by(() => {
		if (activePool) {
			for (const group of grouped) {
				if (group.name === activePool.name) {
					const fs = group.filesystems;
					const volumes = group.volumes;
					const guids = fs
						.map((fs) => ({
							guid: fs.guid
						}))
						.concat(
							volumes.map((volume) => ({
								guid: volume.guid
							}))
						);

					return periodicSnapshots.filter((snapshot) => {
						return guids.some((fs) => fs.guid === snapshot.guid);
					});
				}
			}
		}

		return [];
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
</script>

{#snippet button(type: string)}
	{#if type === 'delete-snapshot' && activeRows && activeRows.length >= 1 && !isPoolSelected}
		<Button
			onclick={() => {
				if (activeRows?.length === 1) {
					modals.snapshot.delete.open = true;
					modals.snapshot.bulkDelete.open = false;
				} else {
					modals.snapshot.bulkDelete.open = true;
					modals.snapshot.delete.open = false;
				}
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

				<span>{activeRows?.length === 1 ? 'Delete Snapshot' : 'Delete Snapshots'}</span>
			</div>
		</Button>
	{/if}

	{#if type === 'view-periodics' && activePool && activePeriodics && activePeriodics.length > 0}
		<Button
			onclick={() => {
				modals.snapshot.periodics.open = true;
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--clock-time-four] mr-1 h-4 w-4"></span>
				<span>View Periodics</span>
			</div>
		</Button>
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
		{@render button('view-periodics')}
	</div>

	<TreeTable
		data={tableData}
		name={tableName}
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={true}
	/>
</div>

<!-- Create Snapshot -->
{#if modals.snapshot.create.open}
	<CreateDetailed bind:open={modals.snapshot.create.open} {pools} {datasets} bind:reload />
{/if}

<!-- Delete Snapshot -->
{#if modals.snapshot.delete.open && activeDatasets && activeDatasets.length === 1}
	<DeleteSnapshot bind:open={modals.snapshot.delete.open} dataset={activeDatasets[0]} bind:reload />
{/if}

<!-- Bulk delete -->
{#if modals.snapshot.bulkDelete.open && activeDatasets && activeDatasets.length > 0}
	<AlertDialogModal
		bind:open={modals.snapshot.bulkDelete.open}
		customTitle={`Are you sure you want to delete ${activeDatasets.length} snapshot${activeDatasets.length > 1 ? 's' : ''}? This action cannot be undone.`}
		actions={{
			onConfirm: async () => {
				const response = await bulkDelete(activeDatasets);
				reload = true;
				if (response.status === 'success') {
					toast.success(
						`Deleted ${activeDatasets.length} snapshot${activeDatasets.length > 1 ? 's' : ''}`,
						{
							position: 'bottom-center'
						}
					);
				} else {
					handleAPIError(response);
					toast.error('Failed to delete snapshots', {
						position: 'bottom-center'
					});
				}

				modals.snapshot.bulkDelete.open = false;
			},
			onCancel: () => {
				modals.snapshot.bulkDelete.open = false;
			}
		}}
	/>
{/if}

{#if modals.snapshot.periodics.open && activePeriodics && activePeriodics.length > 0}
	<Jobs
		bind:open={modals.snapshot.periodics.open}
		{pools}
		{datasets}
		periodicSnapshots={activePeriodics}
		bind:reload
	/>
{/if}
