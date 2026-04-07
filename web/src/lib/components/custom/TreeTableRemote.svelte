<script lang="ts">
	import { storage } from '$lib';
	import type { Column, Row, TreeTableState } from '$lib/types/components/tree-table';
	import { sha256 } from '$lib/utils/string';
	import { findRow, getAllRows } from '$lib/utils/tree-table';
	import { watch, Debounced } from 'runed';
	import { onMount, untrack } from 'svelte';
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
		ajaxURL?: string;
		name: string;
		parentActiveRow?: Row[] | null;
		query?: string;
		multipleSelect?: boolean;
		extraParams?: Record<string, string | number>;
		customPlaceholder?: string;
		initialSort?: { column: string; dir: 'asc' | 'desc' }[];
		reload: boolean;
	}

	let {
		data,
		name,
		parentActiveRow = $bindable([]),
		query = $bindable(),
		multipleSelect = true,
		ajaxURL,
		extraParams = {},
		customPlaceholder = 'No data available',
		initialSort,
		reload = $bindable()
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
	let scroll = $state([0, 0]);
	let hash = $state('');

	function updateParentActiveRows() {
		if (tableInitialized) {
			parentActiveRow = table?.getSelectedRows().map((r) => r.getData() as Row) || [];
		}
	}

	$effect(() => {
		if (data.rows) {
			untrack(async () => {
				if (query && query !== '') return;

				if (data.rows.length === 0) {
					table?.clearData();
					return;
				}

				const selectedIds = table?.getSelectedRows().map((row) => row.getData().id) || [];
				const treeExpands = getAllRows(table?.getRows() || []).map((row) => ({
					id: row.getData().id,
					expanded: row.isTreeExpanded()
				}));

				selectedIds.forEach((id) => {
					const row = findRow(table?.getRows() || [], id);
					if (row) row.select();
				});

				treeExpands.forEach((treeExpand) => {
					const row = findRow(table?.getRows() || [], treeExpand.id);
					if (row) {
						treeExpand.expanded ? row.treeExpand() : row.treeCollapse();
					}
				});

				updateParentActiveRows();
			});
		}
	});

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);

		if (tableComponent) {
			table = new Tabulator(tableComponent, {
				ajaxURL: ajaxURL ? ajaxURL : undefined,
				ajaxResponse: function (url, params, response) {
					return response.data;
				},
				ajaxParams: {
					hash,
					...extraParams
				},
				reactiveData: true,
				columns: data.columns as ColumnDefinition[],
				layout: 'fitColumns',
				selectableRows: multipleSelect ? true : 1,
				dataTreeChildIndent: 16,
				dataTree: true,
				dataTreeChildField: 'children',
				dataTreeStartExpanded: true,
				persistenceID: name,
				paginationMode: 'remote',
				persistence: {
					sort: true,
					page: true,
					filter: true
				},
				placeholder: customPlaceholder || 'No data available',
				pagination: true,
				paginationSize: 25,
				paginationCounter: 'pages',
				sortMode: 'remote',
				filterMode: 'remote',
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

			const widths = tableState.current.columnWidths || {};
			const persistedHidden = tableState.current.hiddenColumns || {};
			table?.getColumns().forEach((col) => {
				const field = col.getField() as string;
				const width = widths[field];
				if (width) {
					col.setWidth(width);
				}
				if (field in persistedHidden) {
					if (persistedHidden[field]) {
						col.hide();
					} else {
						col.show();
					}
				}
			});
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

			if ((column.getDefinition() as any).copyOnClick && value) {
				navigator.clipboard.writeText(value.toString());
				const truncated =
					value.toString().length > 20 ? value.toString().slice(0, 20) + '...' : value.toString();
				toast.success(`Copied "${truncated}" to clipboard`, {
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

			tableState.current = {
				...tableState.current,
				columnWidths: colWidths
			};
		});
	});

	const debouncedQuery = new Debounced(() => query, 300);

	watch(
		() => debouncedQuery.current,
		(newQuery) => {
			if (table && tableInitialized) {
				table.setData(ajaxURL!, {
					hash,
					...extraParams,
					search: newQuery || ''
				});
			}
		}
	);

	watch(
		() => reload,
		(newReload) => {
			if (newReload) {
				table?.setData(ajaxURL!, {
					hash,
					...extraParams,
					search: query || ''
				});
				reload = false;
			}
		}
	);
</script>

<ContextMenu.Root>
	<ContextMenu.Trigger class="flex flex-1">
		<div
			bind:this={tableComponent}
			class="flex-1 cursor-pointer s-tree-table-container"
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
