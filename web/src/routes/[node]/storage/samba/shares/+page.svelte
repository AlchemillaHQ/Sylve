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
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	const results = useQueries([
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
			queryKey: 'samba-shares',
			queryFn: async () => {
				return await getSambaShares();
			},
			keepPreviousData: false,
			initialData: data.shares,
			onSuccess: (data: SambaShare[]) => {
				updateCache('samba-shares', data);
			}
		},
		{
			queryKey: 'groups',
			queryFn: async () => {
				return (await listGroups()) as Group[];
			},
			keepPreviousData: true,
			initialData: data.groups,
			onSuccess: (data: Group[]) => {
				updateCache('groups', data);
			}
		}
	]);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('zfs-datasets');
			queryClient.refetchQueries('samba-shares');
			queryClient.refetchQueries('groups');

			untrack(() => {
				reload = false;
			});
		}
	});

	let datasets: Dataset[] = $derived($results[0].data as Dataset[]);
	let shares: SambaShare[] = $derived($results[1].data as SambaShare[]);
	let groups: Group[] = $derived($results[2].data as Group[]);
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

	let tableData = $derived(generateTableData(shares, datasets, groups));
</script>

{#snippet button(type: string)}
	{#if activeRows !== null && activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				onclick={() => {
					properties.edit.open = true;
					properties.edit.share =
						shares.find((share) => share.id === Number(activeRow?.id)) || null;
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
	<Share bind:open={properties.create.open} {shares} {datasets} {groups} bind:reload />
{/if}

{#if properties.edit.open}
	<Share
		bind:open={properties.edit.open}
		{shares}
		{datasets}
		{groups}
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
