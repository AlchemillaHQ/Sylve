<script lang="ts">
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { findRow, getAllRows, pruneEmptyChildren } from '$lib/utils/tree-table';
	import { onMount, untrack } from 'svelte';
	import {
		TabulatorFull as Tabulator,
		type ColumnDefinition,
		type RowComponent
	} from 'tabulator-tables';

	let tableComponent: HTMLDivElement | null = null;
	let table: Tabulator | null = $state(null);

	interface Props {
		data: {
			rows: Row[];
			columns: Column[];
		};
		name: string;
		parentActiveRow?: Row | null;
	}

	let { data, name, parentActiveRow = $bindable() }: Props = $props();

	function selectParentActiveRow(row: RowComponent) {
		const expandedRow = row.getData();
		for (const column of data.columns) {
			parentActiveRow = {
				...parentActiveRow,
				[`${column.field}`]: expandedRow[column.field],
				id: expandedRow.id ?? parentActiveRow?.id ?? 0
			};
		}
	}

	$effect(() => {
		if (data.rows) {
			untrack(async () => {
				const selectedIds = table?.getSelectedRows().map((row) => row.getData().id) || [];
				const treeExpands =
					getAllRows(table?.getRows() || []).map((row) => ({
						id: row.getData().id,
						expanded: row.isTreeExpanded()
					})) || [];

				await table?.replaceData(pruneEmptyChildren(data.rows));

				selectedIds.forEach((id) => {
					const row = findRow(table?.getRows() || [], id);
					if (row) {
						row.select();
						selectParentActiveRow(row);
					}
				});

				treeExpands?.forEach((treeExpand) => {
					if (treeExpand.expanded) {
						const row = findRow(table?.getRows() || [], treeExpand.id);
						if (row) {
							row.treeExpand();
						}
					} else {
						const row = findRow(table?.getRows() || [], treeExpand.id);
						if (row) {
							row.treeCollapse();
						}
					}
				});
			});
		}
	});

	onMount(() => {
		if (tableComponent) {
			table = new Tabulator(tableComponent, {
				data: pruneEmptyChildren(data.rows),
				reactiveData: true,
				columns: data.columns as ColumnDefinition[],
				layout: 'fitColumns',
				selectableRows: 1,
				dataTreeChildIndent: 16,
				dataTree: true,
				dataTreeChildField: 'children',
				persistence: {
					sort: true
				},
				placeholder: 'No data available'
			});
		}

		table?.on('rowSelected', function (row: RowComponent) {
			selectParentActiveRow(row);
		});

		table?.on('rowDeselected', function (row: RowComponent) {
			parentActiveRow = null;
		});
	});
</script>

<div class="flex h-full flex-col">
	<div bind:this={tableComponent} class="flex-1 overflow-auto" id={name}></div>
</div>
