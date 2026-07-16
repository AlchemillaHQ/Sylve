<script lang="ts">
	import { storage } from '$lib';
	import type { Column, Row, TreeTableState } from '$lib/types/components/tree-table';
	import { sha256 } from '$lib/utils/string';
	import { findRow, getAllRows } from '$lib/utils/tree-table';
	import { watch, Debounced } from 'runed';
	import { onDestroy, onMount, untrack } from 'svelte';
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
		extraParams?: Record<string, string | number | undefined>;
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

	const MIN_PAGE_SIZE = 10;
	const MAX_PAGE_SIZE = 100;
	const DEFAULT_ROW_HEIGHT = 42;
	const HEADER_FOOTER_OVERHEAD = 80;

	let currentPageSize = 25;
	let resizeObserver: ResizeObserver | null = null;
	let resizeTimer: ReturnType<typeof setTimeout> | null = null;
	let observerPrimed = false;
	let lastObservedHeight = 0;

	function clampPageSize(value: number): number {
		return Math.max(MIN_PAGE_SIZE, Math.min(MAX_PAGE_SIZE, value));
	}

	function estimateInitialPageSize(): number {
		const height = tableComponent?.clientHeight ?? 0;
		const usable = height - HEADER_FOOTER_OVERHEAD;
		if (usable <= 0) return 25;
		return clampPageSize(Math.floor(usable / DEFAULT_ROW_HEIGHT));
	}

	function adaptPageSize() {
		if (!table || !tableInitialized) return;

		const holder = tableComponent?.querySelector('.tabulator-tableholder') as HTMLElement | null;
		if (!holder) return;

		const holderHeight = holder.clientHeight;
		if (holderHeight <= 0) return;

		const rowEl = tableComponent?.querySelector('.tabulator-row') as HTMLElement | null;
		const rowHeight = rowEl?.offsetHeight || DEFAULT_ROW_HEIGHT;
		if (rowHeight <= 0) return;

		const size = clampPageSize(Math.floor(holderHeight / rowHeight));
		if (size !== currentPageSize) {
			currentPageSize = size;
			table.setPageSize(size);
		}
	}

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
						if (treeExpand.expanded) {
							row.treeExpand();
						} else {
							row.treeCollapse();
						}
					}
				});

				updateParentActiveRows();
			});
		}
	});

	// https://10.10.30.103/ares/storage/zfs/datasets/snapshots
	let hostname = new URL(location.href).pathname.split('/').filter(Boolean)[0];

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);

		/*
        export interface AjaxContentType {
    headers: JSONRecord;
    body: (url: string, config: any, params: any) => any;
}
        */

		if (tableComponent) {
			const initialPageSize = estimateInitialPageSize();
			currentPageSize = initialPageSize;
			table = new Tabulator(tableComponent, {
				ajaxURL: ajaxURL ? ajaxURL : undefined,
				height: '100%',
				ajaxResponse: function (url, params, response) {
					return response.data;
				},
				ajaxParams: () => ({
					hash,
					...extraParams,
					search: query || ''
				}),
				ajaxConfig: {
					method: 'GET',
					headers: {
						...(hostname && {
							'X-Current-Hostname': hostname
						})
					}
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
				paginationSize: initialPageSize,
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

			resizeObserver = new ResizeObserver((entries) => {
				const newHeight = entries[0]?.contentRect.height ?? 0;
				if (!observerPrimed) {
					observerPrimed = true;
					lastObservedHeight = newHeight;
					return;
				}
				if (Math.abs(newHeight - lastObservedHeight) < DEFAULT_ROW_HEIGHT / 2) return;
				lastObservedHeight = newHeight;
				if (resizeTimer) {
					clearTimeout(resizeTimer);
				}
				resizeTimer = setTimeout(adaptPageSize, 200);
			});
			if (tableComponent) {
				resizeObserver.observe(tableComponent);
			}
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

			if ((column.getDefinition() as unknown as { copyOnClick?: boolean }).copyOnClick && value) {
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

	onDestroy(() => {
		if (resizeTimer) {
			clearTimeout(resizeTimer);
		}
		resizeObserver?.disconnect();
		resizeObserver = null;
	});

	const debouncedQuery = new Debounced(() => query, 300);

	watch(
		() => debouncedQuery.current,
		() => {
			if (table && tableInitialized) {
				table.setData(ajaxURL!);
			}
		}
	);

	watch(
		() => parentActiveRow,
		(rows) => {
			if (!table || !tableInitialized) return;
			const selected = table.getSelectedRows();
			if ((!rows || rows.length === 0) && selected.length > 0) {
				table.deselectRow();
			}
		}
	);

	watch(
		() => reload,
		(newReload) => {
			if (newReload) {
				table?.setData(ajaxURL!);
				reload = false;
			}
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
	.s-tree-table-container {
		display: flex;
		flex-direction: column;
	}

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
