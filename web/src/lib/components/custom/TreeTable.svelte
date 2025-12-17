<script lang="ts">
	import type { Column, Row, TablePreferences } from '$lib/types/components/tree-table';
	import { hasRowsChanged, matchAny, restoreTreeState } from '$lib/utils/table';
	import { findRow, getAllRows, pruneEmptyChildren } from '$lib/utils/tree-table';
	import { deepEqual } from 'fast-equals';
	import { watch, Debounced } from 'runed';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		TabulatorFull as Tabulator,
		type ColumnDefinition,
		type RowComponent
	} from 'tabulator-tables';
	import { PersistedState } from 'runed';

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

	const tablePrefs = new PersistedState(`${name}-prefs`, null as TablePreferences);

	let tableHolder: HTMLDivElement | null = null;
	let tableInitialized = $state(false);

	function updateParentActiveRows() {
		if (tableInitialized) {
			parentActiveRow = table?.getSelectedRows().map((r) => r.getData() as Row) || [];
		}
	}

	async function replaceDataPreservingScroll(rows: Row[]) {
		var tableEl = table?.element as HTMLElement;
		var holder = tableEl.querySelector('.tabulator-tableholder') as HTMLElement;
		if (!holder) {
			await table?.replaceData(rows);
			return;
		}

		var parent = holder.parentElement as HTMLElement;
		var prevScrollTop = holder.scrollTop;
		var prevScrollLeft = holder.scrollLeft;
		var prevMinHeight = parent.style.minHeight;

		parent.style.minHeight = `${parent.offsetHeight}px`;
		parent.style.overflowAnchor = 'none';

		table?.replaceData(rows);

		const newHolder = tableEl.querySelector('.tabulator-tableholder') as HTMLElement;

		parent.style.minHeight = prevMinHeight;

		if (newHolder) {
			newHolder.scrollTop = prevScrollTop;
			newHolder.scrollLeft = prevScrollLeft;
		}
	}

	watch(
		() => data.rows,
		(newRows, oldRows) => {
			if (newRows.length === 0) {
				table?.clearData();
				return;
			}

			if (deepEqual(newRows, oldRows)) {
				return;
			}

			const selectedIds = table?.getSelectedRows().map((r) => r.getData().id) ?? [];
			const expandMap = new Map<number, boolean>(
				getAllRows(table?.getRows() || []).map((r) => [r.getData().id, r.isTreeExpanded()])
			);

			if (hasRowsChanged(table, newRows) && tableInitialized) {
				replaceDataPreservingScroll(pruneEmptyChildren(newRows)).then(() => {
					tableFilter(query || '');
				});
			}

			for (const id of selectedIds) {
				findRow(table?.getRows() || [], id)?.select();
			}

			restoreTreeState(expandMap, table?.getRows() || []);
			updateParentActiveRows();
		}
	);

	onMount(() => {
		if (tableComponent) {
			table = new Tabulator(tableComponent, {
				data: pruneEmptyChildren(data.rows),
				reactiveData: false,
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

			const prefs = tablePrefs.current;
			if (prefs && prefs.columnWidths) {
				table?.getColumns().forEach((col) => {
					const width = prefs.columnWidths[col.getField() as string];
					if (width) {
						col.setWidth(width);
					}
				});
			}
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

		table?.on('columnResized', () => {
			const colWidths: Record<string, number> = {};

			table?.getColumns().forEach((col) => {
				colWidths[col.getField() as string] = col.getWidth();
			});

			tablePrefs.current = { columnWidths: colWidths };
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
	const debouncedQuery = new Debounced(() => query, 300);

	watch(
		() => debouncedQuery.current,
		(newQuery) => {
			tableFilter(newQuery || '');
		}
	);
</script>

<div
	bind:this={tableComponent}
	class="flex-1 cursor-pointer s-tree-table-container"
	id={name}
></div>
