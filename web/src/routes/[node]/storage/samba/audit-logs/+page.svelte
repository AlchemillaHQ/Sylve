<script lang="ts">
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import type { Column } from '$lib/types/components/tree-table';
	import type { SambaShare } from '$lib/types/samba/shares';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		datasets: Dataset[];
		shares: SambaShare[];
	}

	let { data }: { data: Data } = $props();

	const actionPresentation: Record<string, { icon: string; label: string }> = {
		connect: { icon: 'mdi:lan-connect', label: 'Connect' },
		disconnect: { icon: 'mdi:lan-disconnect', label: 'Disconnect' },
		create_file: { icon: 'mdi:file-plus', label: 'Create File' },
		mkdirat: { icon: 'mdi:folder-plus-outline', label: 'Create Directory' },
		unlinkat: { icon: 'mdi:delete-outline', label: 'Delete File/Directory' },
		renameat: { icon: 'mdi:rename-box-outline', label: 'Rename' },
		openat: { icon: 'mdi:file-lock-open-outline', label: 'Open File' },
		close: { icon: 'mdi:file-lock-outline', label: 'Close File' },
		read: { icon: 'mdi:file-eye-outline', label: 'Read File' },
		write: { icon: 'mdi:file-edit-outline', label: 'Write File' }
	};

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
		const action = String(cell.getValue() ?? '');
		const presentation = actionPresentation[action];
		return presentation
			? renderWithIcon(presentation.icon, presentation.label)
			: renderWithIcon('mdi:file-question-outline', action || 'Unknown');
	}

	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'share', title: 'Share' },
			{ field: 'user', title: 'User' },
			{ field: 'client', title: 'Client' },
			{ field: 'ip', title: 'Client IP' },
			{ field: 'action', title: 'Action', formatter: actionFormatter },
			{
				field: 'occurrences',
				title: 'Count',
				formatter: (cell: CellComponent) => cell.getValue() || 1
			},
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

	let reload = $state(false);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable name="smb-audit-log-tt" data={table} ajaxURL="/api/samba/audit-logs" bind:reload />
	</div>
</div>
