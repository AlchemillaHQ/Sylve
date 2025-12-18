<script lang="ts">
	import { bulkDelete, deleteFileSystem, getDatasets } from '$lib/api/zfs/datasets';
	import AlertDialogModal from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateFS from '$lib/components/custom/ZFS/datasets/fs/Create.svelte';
	import EditFS from '$lib/components/custom/ZFS/datasets/fs/Edit.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { groupByPoolNames } from '$lib/utils/zfs/dataset/dataset';
	import { generateTableData } from '$lib/utils/zfs/dataset/fs';
	import { toast } from 'svelte-sonner';
	import { resource, IsDocumentVisible } from 'runed';
	import { untrack } from 'svelte';
	import type { BasicSettings } from '$lib/types/system/settings';
	import { getBasicSettings } from '$lib/api/system/settings';

	interface Data {
		settings: BasicSettings;
		datasets: Dataset[];
	}

	let { data }: { data: Data } = $props();
	let tableName = 'tt-zfsDatasets';
	let visible = new IsDocumentVisible();

	const pools = resource(
		() => 'basic-settings',
		async () => {
			const settings = await getBasicSettings();
			updateCache('basic-settings', settings);
			return settings.pools;
		},
		{
			initialValue: data.settings.pools
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

	let grouped = $derived(groupByPoolNames(pools.current, datasets.current));
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

				for (const fs of filesystems) {
					if (fs.name === activeRow.name) {
						return fs;
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

					for (const fs of filesystems) {
						if (fs.name === row.name) {
							datasets.push(fs);
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
						let title = `${activeDatasets.length} dataset${activeDatasets.length > 1 ? 's' : ''}`;
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
