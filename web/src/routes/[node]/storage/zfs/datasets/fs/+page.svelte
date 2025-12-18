<script lang="ts">
	import { bulkDelete, deleteFileSystem, getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialogModal from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateFS from '$lib/components/custom/ZFS/datasets/fs/Create.svelte';
	import EditFS from '$lib/components/custom/ZFS/datasets/fs/Edit.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { groupByPool } from '$lib/utils/zfs/dataset/dataset';
	import { generateTableData } from '$lib/utils/zfs/dataset/fs';
	import { toast } from 'svelte-sonner';
	import { resource, IsDocumentVisible } from 'runed';
	import { untrack } from 'svelte';

	interface Data {
		pools: Zpool[];
		datasets: Dataset[];
	}

	let { data }: { data: Data } = $props();
	let tableName = 'tt-zfsDatasets';
	let visible = new IsDocumentVisible();

	const pools = resource(
		() => 'zfs-pools',
		async (key, prevKey, { signal }) => {
			const result = await getPools();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.pools
		}
	);

	const datasets = resource(
		() => 'zfs-filesystems',
		async (key, prevKey, { signal }) => {
			const result = await getDatasets('filesystem');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.datasets
		}
	);

	let grouped = $derived(groupByPool(pools.current, datasets.current));
	let tableData = $derived(generateTableData(grouped));
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let reload = $state(false);

	$effect(() => {
		if (reload) {
			pools.refetch();
			datasets.refetch();

			reload = false;
		}
	});

	$effect(() => {
		if (visible.current) {
			untrack(() => {
				pools.refetch();
				datasets.refetch();
			});
		}
	});

	let activeDataset: Dataset | null = $derived.by(() => {
		if (activeRow) {
			for (const dataset of grouped) {
				const filesystems = dataset.filesystems;
				const snapshots = dataset.snapshots;

				for (const fs of filesystems) {
					if (fs.name === activeRow.name) {
						return fs;
					}
				}

				for (const snap of snapshots) {
					if (snap.name === activeRow.name) {
						return snap;
					}
				}
			}
		}

		return null;
	});

	let activeDatasets: Dataset[] = $derived.by(() => {
		if (activeRows) {
			let datasets: Dataset[] = [];
			for (const row of activeRows) {
				for (const dataset of grouped) {
					const filesystems = dataset.filesystems;
					const snapshots = dataset.snapshots;

					for (const fs of filesystems) {
						if (fs.name === row.name) {
							datasets.push(fs);
						}
					}

					for (const snap of snapshots) {
						if (snap.name === row.name) {
							datasets.push(snap);
						}
					}
				}
			}
			return datasets;
		}

		return [];
	});

	let poolsSelected = $derived.by(() => {
		if (activeRows && activeRows.length > 0) {
			const filtered = activeRows.filter((row) => {
				return row.type === 'pool';
			});

			return filtered.length > 0;
		}

		return false;
	});

	let query: string = $state('');

	let modals = $state({
		fs: {
			create: {
				open: false
			},
			edit: {
				open: false
			},
			delete: {
				open: false
			}
		},
		bulk: {
			delete: {
				open: false,
				title: ''
			}
		}
	});
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length == 1}
		{#if type === 'edit-filesystem' && activeDataset?.type === GZFSDatasetTypeSchema.enum.FILESYSTEM}
			<Button
				onclick={async () => {
					if (activeDataset) {
						modals.fs.edit.open = true;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

					<span>Edit Filesystem</span>
				</div>
			</Button>
		{/if}

		{#if type === 'delete-filesystem' && activeDataset?.type === GZFSDatasetTypeSchema.enum.FILESYSTEM && activeDataset?.name.includes('/')}
			<Button
				onclick={async () => {
					if (activeDataset) {
						modals.fs.delete.open = true;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete Filesystem</span>
				</div>
			</Button>
		{/if}
	{:else if activeRows && activeRows.length > 1}
		{#if activeDatasets.length > 0 && !poolsSelected}
			{#if type === 'bulk-delete'}
				<Button
					onclick={async () => {
						let [snapLen, fsLen] = [0, 0];
						activeDatasets.forEach((dataset) => {
							if (dataset.type === GZFSDatasetTypeSchema.enum.SNAPSHOT) {
								snapLen++;
							} else if (dataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM) {
								fsLen++;
							}
						});

						let title = '';
						if (snapLen > 0 && fsLen > 0) {
							title = `${snapLen} snapshot${snapLen > 1 ? 's' : ''} and ${fsLen} filesystem${fsLen > 1 ? 's' : ''}`;
						} else if (snapLen > 0) {
							title = `${snapLen} snapshot${snapLen > 1 ? 's' : ''}`;
						} else if (fsLen > 0) {
							title = `${fsLen} filesystem${fsLen > 1 ? 's' : ''}`;
						}

						modals.bulk.delete.open = true;
						modals.bulk.delete.title = title;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
						<span>Delete Datasets</span>
					</div>
				</Button>
			{/if}
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => {
				modals.fs.create.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>
		{@render button('edit-filesystem')}
		{@render button('delete-filesystem')}
		{@render button('bulk-delete')}
	</div>

	<TreeTable
		data={tableData}
		name={tableName}
		bind:parentActiveRow={activeRows}
		multipleSelect={true}
		bind:query
		initialSort={[{ column: 'name', dir: 'asc' }]}
	/>
</div>

<!-- Delete FS -->
{#if modals.fs.delete.open && activeDataset && activeDataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM}
	<AlertDialogModal
		bind:open={modals.fs.delete.open}
		names={{
			parent: 'filesystem',
			element: activeDataset.name
		}}
		actions={{
			onConfirm: async () => {
				if (activeDataset.guid) {
					const response = await deleteFileSystem(activeDataset);
					reload = true;
					if (response.status === 'success') {
						toast.success(`Deleted filesystem ${activeDataset.name}`, {
							position: 'bottom-center'
						});
					} else {
						handleAPIError(response);
						toast.error(`Failed to delete filesystem ${activeDataset.name}`, {
							position: 'bottom-center'
						});
					}
				} else {
					toast.error('Filesystem GUID not found', {
						position: 'bottom-center'
					});
				}

				modals.fs.delete.open = false;
			},
			onCancel: () => {
				modals.fs.delete.open = false;
			}
		}}
	/>
{/if}

<!-- Bulk delete -->
{#if modals.bulk.delete.open && activeDatasets.length > 0}
	<AlertDialogModal
		bind:open={modals.bulk.delete.open}
		customTitle={`Are you sure you want to delete ${modals.bulk.delete.title}? This action cannot be undone.`}
		actions={{
			onConfirm: async () => {
				const response = await bulkDelete(activeDatasets);
				reload = true;
				if (response.status === 'success') {
					toast.success(`Deleted ${activeDatasets.length} datasets`, {
						position: 'bottom-center'
					});
				} else {
					handleAPIError(response);
					toast.error('Failed to delete datasets', {
						position: 'bottom-center'
					});
				}

				modals.bulk.delete.open = false;
			},
			onCancel: () => {
				modals.bulk.delete.open = false;
			}
		}}
	/>
{/if}

<!-- Create FS -->
{#if modals.fs.create.open}
	<CreateFS bind:open={modals.fs.create.open} datasets={datasets.current} {grouped} bind:reload />
{/if}

<!-- Edit FS -->
{#if modals.fs.edit.open && activeDataset && activeDataset.type === GZFSDatasetTypeSchema.enum.FILESYSTEM}
	<EditFS bind:open={modals.fs.edit.open} dataset={activeDataset} bind:reload />
{/if}
