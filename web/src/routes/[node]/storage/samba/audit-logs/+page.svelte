<script lang="ts">
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import { storage } from '$lib';
	import type { Column } from '$lib/types/components/tree-table';
	import type { SambaShare } from '$lib/types/samba/shares';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { sha256 } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		datasets: Dataset[];
		shares: SambaShare[];
	}

	let { data }: { data: Data } = $props();

	function pathFormatter(cell: CellComponent) {
		const row = cell.getRow();
		const share = data.shares.find((s) => s.name === row.getData().share);
		if (share) {
			const dataset = data.datasets.find((d) => d.guid === share.dataset);
			if (dataset?.mountpoint) {
				const path = cell.getValue().replace(dataset.mountpoint, '');
				return path || '-';
			}
		}

		return cell.getValue() || '-';
	}

	function actionFormatter(cell: CellComponent) {
		const action = cell.getValue();
		switch (action) {
			case 'mkdirat':
				return renderWithIcon('mdi:create-new-folder-outline', 'Create Directory');
			case 'unlinkat':
				return renderWithIcon('mdi:delete-outline', 'Delete (File/Directory)');
			case 'create_file':
				return renderWithIcon('mdi:file-plus', 'Create File');
			case 'renameat':
				return renderWithIcon('mdi:rename', 'Rename');
			default:
				return renderWithIcon('mdi:file', action);
		}
	}

	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'share', title: 'Share' },
			{ field: 'action', title: 'Action', formatter: actionFormatter },
			{
				field: 'path',
				title: 'Path',
				formatter: pathFormatter
			},
			{
				field: 'target',
				title: 'Target',
				formatter: pathFormatter
			},
			{
				field: 'createdAt',
				title: 'Date',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return convertDbTime(value);
				}
			}
		] as Column[],
		rows: []
	});

	let hash = $state('');
	let reload = $state(false);

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-full flex-col overflow-hidden">
		{#if hash}
			<TreeTable
				name={'smb-audit-log-tt'}
				data={table}
				ajaxURL="/api/samba/audit-logs?hash={hash}"
				bind:reload
			/>
		{/if}
	</div>
</div>
