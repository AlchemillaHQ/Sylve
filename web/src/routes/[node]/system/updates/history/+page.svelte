<script lang="ts">
	import Controls from '$lib/components/custom/System/Updates/Controls.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { hostUpdateActionLabels, type HostUpdateContext } from '$lib/types/system/updates';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { watch } from 'runed';
	import { getContext } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';

	const updates = getContext<HostUpdateContext>('hostUpdates');
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	const selectedId = $derived(activeRows?.length === 1 ? Number(activeRows[0].id) : 0);
	watch(
		() => updates.node,
		() => {
			activeRows = null;
			query = '';
		}
	);
	function statusFormatter(cell: CellComponent) {
		const status = String(cell.getValue());
		return renderWithIcon(
			status === 'success' ? 'mdi:check-circle-outline' : 'mdi:alert-circle-outline',
			escapeHTML(status === 'success' ? 'Completed' : 'Failed'),
			status === 'success' ? '' : 'text-destructive'
		);
	}
	const columns: Column[] = [
		{ field: 'id', title: 'Operation', formatter: 'plaintext', sorter: 'number' },
		{ field: 'actionLabel', title: 'Action', formatter: 'plaintext' },
		{ field: 'status', title: 'Status', formatter: statusFormatter },
		{ field: 'actor', title: 'Requested by', formatter: 'plaintext' },
		{
			field: 'createdAt',
			title: 'Started',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{
			field: 'finishedAt',
			title: 'Finished',
			visible: false,
			formatter: (cell: CellComponent) => (cell.getValue() ? convertDbTime(cell.getValue()) : '—')
		}
	];
	const tableData = $derived({
		columns,
		rows: (updates.view?.history ?? []).map((op) => ({
			...op,
			actionLabel: hostUpdateActionLabels[op.action] ?? op.action
		}))
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => updates.openOperation(selectedId)}
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!selectedId}
			title="View selected operation"
		>
			<SpanWithIcon icon="icon-[mdi--eye]" size="h-4 w-4" gap="gap-2" title="Details" />
		</Button>
		<div class="ml-auto flex items-center gap-2"><Controls /></div>
	</div>
	<div class="flex h-full flex-col overflow-hidden">
		{#key updates.node}<TreeTable
				data={tableData}
				name="tt-host-update-history"
				bind:parentActiveRow={activeRows}
				bind:query
				multipleSelect={false}
				dataTree={false}
				initialSort={[{ column: 'id', dir: 'desc' }]}
				customPlaceholder={updates.error || 'No recent operations'}
			/>{/key}
	</div>
</div>
