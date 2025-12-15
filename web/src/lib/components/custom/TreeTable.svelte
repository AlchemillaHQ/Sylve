<script lang="ts">
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { hasRowsChanged, matchAny } from '$lib/utils/table';
	import { findRow, getAllRows, pruneEmptyChildren } from '$lib/utils/tree-table';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
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
		parentActiveRow?: Row[] | null;
		query?: string;
		multipleSelect?: boolean;
		customPlaceholder?: string;
		initialSort?: { column: string; dir: 'asc' | 'desc' }[];
	}

	let {
		data,
		name,
		parentActiveRow = $bindable([]),
		query = $bindable(),
		multipleSelect = true,
		customPlaceholder = 'No data available',
		initialSort
	}: Props = $props();

	let tableHolder: HTMLDivElement | null = null;
	let tableInitialized = $state(false);
	let scrollTop = 0;
	let scrollLeft = 0;
	let shouldPreserveScroll = false;

	let aboutToClick = $state(false);

	function updateParentActiveRows() {
		if (tableInitialized) {
			parentActiveRow = table?.getSelectedRows().map((r) => r.getData() as Row) || [];
		}
	}

	async function replaceDataPreservingScroll(rows: Row[]) {
		if (!tableInitialized) {
			await table?.replaceData(rows);
			return;
		}

		if (tableHolder) {
			scrollTop = tableHolder.scrollTop;
			scrollLeft = tableHolder.scrollLeft;
			shouldPreserveScroll = true;
		}

		await table?.replaceData(rows);
	}

	$effect(() => {
		if (data.rows) {
			untrack(async () => {
				if (data.rows.length === 0) {
					table?.clearData();
					return;
				}

				const selectedIds = table?.getSelectedRows().map((row) => row.getData().id) || [];
				const treeExpands = getAllRows(table?.getRows() || []).map((row) => ({
					id: row.getData().id,
					expanded: row.isTreeExpanded()
				}));

				if (hasRowsChanged(table, data.rows) && !aboutToClick) {
					if (tableInitialized) {
						await replaceDataPreservingScroll(pruneEmptyChildren(data.rows));
						tableFilter(query || '');
					}
				}

				for (let i = 0; i < selectedIds.length; i++) {
					const id = selectedIds[i];
					const row = findRow(table?.getRows() || [], id);
					if (row) row.select();
				}

				const rowMap = new Map<number, RowComponent>();
				const buildRowMap = (rows: RowComponent[]) => {
					for (const row of rows) {
						rowMap.set(row.getData().id, row);
						const children = row.getTreeChildren();
						if (children.length > 0) {
							buildRowMap(children);
						}
					}
				};

				buildRowMap(table?.getRows() || []);

				for (let i = 0; i < treeExpands.length; i++) {
					const treeExpand = treeExpands[i];
					const row = rowMap.get(treeExpand.id);
					if (row) {
						treeExpand.expanded ? row.treeExpand() : row.treeCollapse();
					}
				}

				updateParentActiveRows();
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
				selectableRows: multipleSelect ? true : 1,
				dataTreeChildIndent: 16,
				dataTree: true,
				dataTreeChildField: 'children',
				dataTreeStartExpanded: true,
				persistenceID: name,
				paginationMode: 'local',
				persistence: {
					sort: true,
					page: true,
					filter: true
				},
				placeholder: customPlaceholder || 'No data available',
				pagination: true,
				paginationSize: 25,
				paginationCounter: 'pages',
				initialSort: initialSort ? initialSort : []
			});
		}

		table?.on('rowSelected', updateParentActiveRows);
		table?.on('rowDeselected', updateParentActiveRows);

		table?.on('rowDblClick', (_event: UIEvent, row: RowComponent) => {
			row.toggleSelect();
		});

		table?.on('tableBuilt', () => {
			tableInitialized = true;
			tableHolder = tableComponent?.querySelector(
				'.tabulator-tableholder'
			) as HTMLDivElement | null;

			document.querySelector('.tabulator-footer')?.addEventListener('mouseover', () => {
				aboutToClick = true;
			});

			document.querySelector('.tabulator-footer')?.addEventListener('mouseout', () => {
				aboutToClick = false;
			});
		});

		table?.on('renderComplete', () => {
			if (!shouldPreserveScroll || !tableHolder) return;
			shouldPreserveScroll = false;

			requestAnimationFrame(() => {
				if (!tableHolder) return;
				tableHolder.scrollTop = scrollTop;
				tableHolder.scrollLeft = scrollLeft;
			});
		});

		table?.on('cellClick', (_event: UIEvent, cell) => {
			const value = cell.getValue();
			const column = cell.getColumn();

			if ((column.getDefinition() as any).copyOnClick && value) {
				navigator.clipboard.writeText(value.toString());
				toast.success(`Copied ${value.toString()} to clipboard`, {
					duration: 2000,
					position: 'bottom-center'
				});
			}
		});
	});

	function tableFilter(query: string) {
		if (table && tableInitialized) {
			if (query === '') {
				table.clearFilter(true);
				return;
			}
			table.setFilter(matchAny, { query });
		}
	}

	$effect(() => {
		tableFilter(query || '');
	});
</script>

<div
	bind:this={tableComponent}
	class="flex-1 cursor-pointer s-tree-table-container"
	id={name}
></div>
