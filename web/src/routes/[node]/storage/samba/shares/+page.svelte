<script lang="ts">
	import { listGroups } from '$lib/api/auth/groups';
	import { deleteSambaShare, getSambaShares } from '$lib/api/samba/share';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Share from '$lib/components/custom/Samba/Share.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Group } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { SambaShare } from '$lib/types/samba/shares';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
	}

	let { data }: { data: Data } = $props();

	let datasets = resource(
		() => 'zfs-filesystems',
		async () => {
			const result = await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM);
			updateCache('zfs-filesystems', result);
			return result;
		},
		{
			initialValue: data.datasets
		}
	);

	let shares = resource(
		() => 'samba-shares',
		async () => {
			const result = await getSambaShares();
			updateCache('samba-shares', result);
			return result;
		},
		{
			initialValue: data.shares
		}
	);

	let groups = resource(
		() => 'groups',
		async () => {
			const result = await listGroups();
			updateCache('groups', result);
			return result;
		},
		{
			initialValue: data.groups
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				datasets.refetch();
				shares.refetch();
				groups.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let options = {
		create: {
			open: false
		},
		delete: {
			open: false
		},
		edit: {
			open: false,
			share: null as SambaShare | null
		}
	};

	let properties = $state(options);
	let query = $state('');

	function generateTableData(
		shares: SambaShare[],
		datasets: Dataset[],
		groups: Group[]
	): {
		rows: Row[];
		columns: Column[];
	} {
		function groupFormatter(cell: CellComponent) {
			const groups = cell.getValue() as Group[];
			if (!groups?.length) return '-';

			const shown = groups
				.slice(0, 5)
				.map((g) => g.name)
				.join(', ');
			return groups.length > 5 ? `${shown}, …` : shown;
		}

		const rows: Row[] = [];
		const columns: Column[] = [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'name',
				title: 'Name'
			},
			{
				field: 'mountpoint',
				title: 'Mount Point'
			},
			{
				field: 'readOnlyGroups',
				title: 'Read-Only Groups',
				formatter: groupFormatter
			},
			{
				field: 'writeableGroups',
				title: 'Writeable Groups',
				formatter: groupFormatter
			},
			{
				field: 'created',
				title: 'Created At',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return convertDbTime(value);
				}
			}
		];

		for (const share of shares) {
			const dataset = datasets.find((ds) => ds.guid === share.dataset);
			const row: Row = {
				id: share.id,
				name: share.name,
				mountpoint: dataset ? dataset.mountpoint : '-',
				readOnlyGroups: share.readOnlyGroups || [],
				writeableGroups: share.writeableGroups || [],
				created: share.createdAt
			};

			rows.push(row);
		}

		return {
			rows: rows,
			columns: columns
		};
	}

	let tableData = $derived(generateTableData(shares.current, datasets.current, groups.current));
</script>

{#snippet button(type: string)}
	{#if activeRows !== null && activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				onclick={() => {
					properties.edit.open = true;
					properties.edit.share =
						shares.current.find((share) => share.id === Number(activeRow?.id)) || null;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

					<span>Edit Share</span>
				</div>
			</Button>
		{/if}

		{#if type === 'delete'}
			<Button
				onclick={() => {
					properties.delete.open = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

					<span>Delete Share</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				properties.create.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name={'shares-tt'}
		bind:parentActiveRow={activeRows}
		multipleSelect={true}
		bind:query
	/>
</div>

{#if properties.create.open}
	<Share
		bind:open={properties.create.open}
		shares={shares.current}
		datasets={datasets.current}
		groups={groups.current}
		bind:reload
	/>
{/if}

{#if properties.edit.open}
	<Share
		bind:open={properties.edit.open}
		shares={shares.current}
		datasets={datasets.current}
		groups={groups.current}
		share={properties.edit.share}
		edit={properties.edit.open}
		bind:reload
	/>
{/if}

<AlertDialog
	open={properties.delete.open}
	names={{ parent: 'Samba share', element: activeRow ? activeRow.name : '' }}
	actions={{
		onConfirm: async () => {
			if (activeRow) {
				const response = await deleteSambaShare(Number(activeRow.id));
				if (response.status === 'error') {
					handleAPIError(response);
					toast.error('Failed to delete Samba share', {
						position: 'bottom-center'
					});

					return;
				}

				toast.success('Samba share deleted', {
					position: 'bottom-center'
				});

				properties.delete.open = false;
				activeRows = null;
				reload = true;
			}
		},
		onCancel: () => {
			properties.delete.open = false;
		}
	}}
></AlertDialog>
