<script lang="ts">
	import type { Column, Row, TreeTableState } from '$lib/types/components/tree-table';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import {
		TabulatorFull as Tabulator,
		type ColumnDefinition,
		type RowComponent
	} from 'tabulator-tables';
	import { PersistedState } from 'runed';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';

	let tableComponent: HTMLDivElement | null = null;
	let table: Tabulator | null = $state(null);

	export interface InfiniteTableControl {
		push: (rows: Row[]) => void;
		clear: () => void;
		setFilter: (fn: ((data: Row) => boolean) | null) => void;
	}

	interface Props {
		columns: Column[];
		name: string;
		parentActiveRow?: Row[] | null;
		initialSort?: { column: string; dir: 'asc' | 'desc' }[];
		maxRows?: number;
		idField?: string;
		control?: InfiniteTableControl | null;
	}

	let {
		columns,
		name,
		parentActiveRow = $bindable([]),
		initialSort,
		maxRows = 2000,
		idField = 'id',
		control = $bindable(null)
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	const tableState = new PersistedState<TreeTableState>(`${name}-state`, {
		columnWidths: {},
		expandedRows: {},
		hiddenColumns: {}
	});

	let hiddenColumns = $state<Record<string, boolean>>(tableState.current.hiddenColumns ?? {});

	function isColumnVisible(field: string): boolean {
		if (field in hiddenColumns) {
			return !hiddenColumns[field];
		}
		const col = columns.find((c) => c.field === field);
		return col?.visible !== false;
	}

	function toggleColumnVisibility(field: string) {
		if (!table) return;
		const col = table.getColumn(field);
		if (!col) return;

		if (isColumnVisible(field)) {
			col.hide();
			hiddenColumns = { ...hiddenColumns, [field]: true };
		} else {
			col.show();
			hiddenColumns = { ...hiddenColumns, [field]: false };
		}

		tableState.current = { ...tableState.current, hiddenColumns };
	}

	// Plain (non-reactive) Set — only used imperatively inside push/clear.
	const knownIds = new SvelteSet<string | number>();

	function updateParentActiveRows() {
		parentActiveRow = table?.getSelectedRows().map((r) => r.getData() as Row) || [];
	}

	function pushRows(rows: Row[]) {
		if (!table) return;

		const newRows = rows.filter((r) => !knownIds.has(r[idField]));
		if (newRows.length === 0) return;

		for (const r of newRows) knownIds.add(r[idField]);

		// Prepend so newest rows appear at the top without sorting all data.
		table.addData(newRows, true).then(() => {
			const allData = table?.getData() ?? [];
			if (allData.length > maxRows) {
				// The oldest rows are at the end (appended via earlier pushes that got
				// shifted down). Remove from the tail.
				const toRemove = allData.slice(maxRows);
				for (const row of toRemove) {
					const tRow = table?.getRow(row[idField]);
					if (tRow) {
						knownIds.delete(row[idField]);
						tRow.delete();
					}
				}
			}
		});
	}

	function clearRows() {
		table?.clearData();
		knownIds.clear();
	}

	function applyFilter(fn: ((data: Row) => boolean) | null) {
		if (!table) return;
		if (!fn) {
			table.clearFilter(true);
		} else {
			// eslint-disable-next-line @typescript-eslint/no-explicit-any
			table.setFilter(fn as any);
		}
	}

	onMount(() => {
		if (tableComponent) {
			table = new Tabulator(tableComponent, {
				data: [],
				reactiveData: false,
				columns: columns as ColumnDefinition[],
				layout: 'fitColumns',
				selectableRows: 1,
				renderVertical: 'virtual',
				height: '100%',
				placeholder: 'No log entries captured yet',
				initialSort: initialSort ?? [],
				debugInvalidOptions: false,
				headerFilterLiveFilterDelay: 200
			});
		}

		table?.on('rowSelected', updateParentActiveRows);
		table?.on('rowDeselected', updateParentActiveRows);

		table?.on('rowDblClick', (_event: UIEvent, row: RowComponent) => {
			row.toggleSelect();
		});

		table?.on('tableBuilt', () => {
			const widths = tableState.current.columnWidths || {};
			const persistedHidden = tableState.current.hiddenColumns || {};
			table?.getColumns().forEach((col) => {
				const field = col.getField() as string;
				const width = widths[field];
				if (width) col.setWidth(width);
				if (field in persistedHidden) {
					if (persistedHidden[field]) {
						col.hide();
					} else {
						col.show();
					}
				}
			});

			// Expose the imperative handle to the parent.
			control = { push: pushRows, clear: clearRows, setFilter: applyFilter };
		});

		table?.on('columnResized', () => {
			const colWidths: Record<string, number> = {};
			table?.getColumns().forEach((col) => {
				colWidths[col.getField() as string] = col.getWidth();
			});
			tableState.current = { ...tableState.current, columnWidths: colWidths };
		});

		return () => {
			control = null;
			if (table) {
				table.destroy();
				table = null;
			}
		};
	});
</script>

<ContextMenu.Root>
	<ContextMenu.Trigger class="flex flex-1 min-h-0">
		<div
			bind:this={tableComponent}
			class="flex-1 min-h-0 cursor-default s-tree-table-container"
			id={name}
		></div>
	</ContextMenu.Trigger>
	<ContextMenu.Content>
		<ContextMenu.Label>Columns</ContextMenu.Label>
		<ContextMenu.Separator />
		{#each columns.filter((c) => !!c.field && !!c.title && c.title[0] !== c.title[0].toLowerCase()) as col (col.field)}
			<ContextMenu.CheckboxItem
				checked={isColumnVisible(col.field)}
				onCheckedChange={() => toggleColumnVisibility(col.field)}
				closeOnSelect={false}
			>
				{col.title}
			</ContextMenu.CheckboxItem>
		{/each}
	</ContextMenu.Content>
</ContextMenu.Root>
