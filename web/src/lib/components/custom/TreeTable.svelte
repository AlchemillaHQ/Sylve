<script lang="ts">
	import type { Column, Row, TreeTableState } from '$lib/types/components/tree-table';
	import { hasRowsChanged, matchAny } from '$lib/utils/table';
	import { getAllRows, pruneEmptyChildren } from '$lib/utils/tree-table';
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
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';

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
		movable?: boolean;
		onRowMoved?: (rows: Row[]) => void;
		persistSort?: boolean;
	}

	let {
		data,
		name,
		parentActiveRow = $bindable([]),
		query = $bindable(),
		multipleSelect = true,
		customPlaceholder = 'No data available',
		initialSort,
		movable = false,
		onRowMoved,
		persistSort = true
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	const tableState = new PersistedState<TreeTableState>(`${name}-state`, {
		columnWidths: {},
		expandedRows: {},
		hiddenColumns: {}
	});

	// hiddenColumns values: true = hidden, false = explicitly shown (overrides visible:false default)
	// missing key = use column definition's visible property
	let hiddenColumns = $state<Record<string, boolean>>(tableState.current.hiddenColumns ?? {});

	function isColumnVisible(field: string): boolean {
		if (field in hiddenColumns) {
			return !hiddenColumns[field];
		}
		const col = data.columns.find((c) => c.field === field);
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

	let tableHolder: HTMLDivElement | null = null;
	let tableInitialized = $state(false);
	let restoringState = false;
	let restoringSelection = false;
	let scroll = $state([0, 0]);
	let restoreSelected = $state(false);
	let selectedIds = $state<Set<string | number>>(new Set());

	function saveExpandedState() {
		if (!table || restoringState) return;

		const expanded: Record<string | number, boolean> = {};
		for (const row of getAllRows(table.getRows())) {
			const rowData = row.getData();
			if (row.isTreeExpanded()) {
				expanded[rowData.id] = true;
			}
		}

		tableState.current = {
			...tableState.current,
			expandedRows: expanded
		};
	}

	function restoreExpandedState() {
		if (!table) return;

		restoringState = true;

		const expandedRows = tableState.current.expandedRows || {};

		for (const row of getAllRows(table.getRows())) {
			const rowData = row.getData();

			if (rowData.children) {
				if (expandedRows[rowData.id]) {
					row.treeExpand();
				} else {
					row.treeCollapse();
				}
			}
		}

		restoringState = false;
	}

	function updateParentActiveRows() {
		if (tableInitialized && !restoringState && !restoringSelection) {
			parentActiveRow = table?.getSelectedRows().map((r) => r.getData() as Row) || [];
		}
	}

	function selectRowsRecursively(selectedIds: Set<string | number>) {
		if (!table) return;
		const allRows = getAllRows(table.getRows());

		for (const row of allRows) {
			const rowData = row.getData();
			if (selectedIds.has(rowData.id)) {
				try {
					row.select();
				} catch (e) {
					console.warn(`Could not select row with id ${rowData.id}:`, e);
				}
			}
		}
	}

	async function replaceDataPreservingScroll(rows: Row[]) {
		const tableEl = table?.element as HTMLElement;
		const holder = tableEl?.querySelector('.tabulator-tableholder') as HTMLElement;

		if (!holder) {
			await table?.replaceData(rows);
			return;
		}

		const parent = holder.parentElement as HTMLElement;
		const prevScrollTop = holder.scrollTop;
		const prevScrollLeft = holder.scrollLeft;
		const prevMinHeight = parent.style.minHeight;

		parent.style.minHeight = `${parent.offsetHeight}px`;
		parent.style.overflowAnchor = 'none';

		await table?.replaceData(rows);

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

			selectedIds = new Set(table?.getSelectedRows().map((r) => r.getData().id) ?? []);

			if (hasRowsChanged(table, newRows) && tableInitialized) {
				replaceDataPreservingScroll(pruneEmptyChildren(newRows)).then(async () => {
					restoringSelection = true;

					tableFilter(query || '');
					restoreExpandedState();

					restoreSelected = true;
				});
			}
		}
	);

	watch(
		() => restoreSelected,
		(value) => {
			if (value) {
				selectRowsRecursively(selectedIds);
				restoringSelection = false;
				updateParentActiveRows();
				restoreSelected = false;
				selectedIds = new Set();
			}
		}
	);

	onMount(() => {
		if (tableComponent) {
			table = new Tabulator(tableComponent, {
				data: pruneEmptyChildren(data.rows),
				reactiveData: false,
				columns: data.columns as ColumnDefinition[],
				layout: 'fitDataStretch',
				selectableRows: multipleSelect ? true : 1,
				dataTreeChildIndent: 16,
				dataTree: true,
				dataTreeChildField: 'children',
				dataTreeStartExpanded: false,
				persistenceID: name,
				paginationMode: 'local',
				persistence: {
					sort: persistSort,
					page: true,
					filter: true
				},
				placeholder: customPlaceholder || 'No data available',
				pagination: true,
				paginationSize: 25,
				paginationCounter: 'pages',
				initialSort: initialSort ? initialSort : [],
				debugInvalidOptions: false,
				...(movable
					? {
							movableRows: true,
							rowHeader: {
								headerSort: false,
								resizable: false,
								minWidth: 30,
								width: 30,
								rowHandle: true,
								formatter: 'handle'
							}
						}
					: {})
			});
		}

		table?.on('rowSelected', updateParentActiveRows);
		table?.on('rowDeselected', updateParentActiveRows);

		if (movable) {
			table?.on('rowMoved', () => {
				const rows = (table?.getData() as Row[]) ?? [];
				onRowMoved?.(rows);
			});
		}

		table?.on('rowDblClick', (_event: UIEvent, row: RowComponent) => {
			row.toggleSelect();
		});

		table?.on('tableBuilt', () => {
			tableInitialized = true;
			tableHolder = tableComponent?.querySelector(
				'.tabulator-tableholder'
			) as HTMLDivElement | null;

			const widths = tableState.current.columnWidths || {};
			const persistedHidden = tableState.current.hiddenColumns || {};
			table?.getColumns().forEach((col) => {
				const field = col.getField() as string;
				const width = widths[field];
				if (width) {
					col.setWidth(width);
				}
				if (field in persistedHidden) {
					// true = explicitly hidden, false = explicitly shown (overrides visible:false)
					if (persistedHidden[field]) {
						col.hide();
					} else {
						col.show();
					}
				}
				// no entry → leave Tabulator's default (from column definition) as-is
			});

			restoreExpandedState();
		});

		table?.on('scrollVertical', (top) => {
			scroll = [top, scroll[1]];
		});

		table?.on('scrollHorizontal', (left) => {
			scroll = [scroll[0], left];
		});

		table?.on('renderComplete', () => {
			const container = tableComponent?.querySelector('.tabulator-tableholder') as HTMLDivElement;
			if (container) {
				container.scrollTop = scroll[0];
				container.scrollLeft = scroll[1];
			}
		});

		table?.on('cellClick', (_event: UIEvent, cell) => {
			const value = cell.getValue();
			const column = cell.getColumn();
			const colDef = column.getDefinition() as Column;
			const rowData = cell.getRow().getData();

			const shouldCopy =
				typeof colDef.copyOnClick === 'function'
					? colDef.copyOnClick(cell.getRow())
					: !!colDef.copyOnClick;

			if (shouldCopy && value !== undefined && value !== null) {
				const textToCopy = colDef.copyValue
					? colDef.copyValue(cell)
					: String(rowData.toCopy ?? value);

				navigator.clipboard
					.writeText(textToCopy)
					.then(() => {
						toast.success(`Copied "${textToCopy}" to clipboard`, {
							duration: 2000,
							position: 'bottom-center'
						});
					})
					.catch((err) => {
						console.error('Failed to copy text:', err);
					});
			}
		});

		table?.on('columnResized', () => {
			console.log('Column resized, saving widths...');

			const colWidths: Record<string, number> = {};

			table?.getColumns().forEach((col) => {
				colWidths[col.getField() as string] = col.getWidth();
			});

			tableState.current = {
				...tableState.current,
				columnWidths: colWidths
			};
		});

		table?.on('dataTreeRowCollapsed', () => {
			saveExpandedState();
		});

		table?.on('dataTreeRowExpanded', () => {
			saveExpandedState();
		});

		return () => {
			if (table) {
				table.destroy();
				table = null;
			}

			tableInitialized = false;
		};
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

<ContextMenu.Root>
	<ContextMenu.Trigger class="flex flex-1 min-h-0">
		<div
			bind:this={tableComponent}
			class="flex-1 min-h-0 cursor-pointer s-tree-table-container"
			id={name}
		></div>
	</ContextMenu.Trigger>
	<ContextMenu.Content>
		<ContextMenu.Label>Columns</ContextMenu.Label>
		<ContextMenu.Separator />
		{#each data.columns.filter((c) => !!c.field && !!c.title && c.title[0] !== c.title[0].toLowerCase()) as col (col.field)}
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

<style>
	:global(.s-tree-table-container > .tabulator) {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
	}

	:global(.s-tree-table-container > .tabulator > .tabulator-tableholder) {
		flex: 1;
		min-height: 0;
	}
</style>
