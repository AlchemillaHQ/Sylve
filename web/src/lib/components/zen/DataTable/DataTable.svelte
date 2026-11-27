<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts" generics="TData extends RowData">
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import {
		createTable,
		FlexRender,
		type ColumnDef,
		type FilterFn,
		type Row,
		type RowData,
		type RowSelectionState,
		type SortingState
	} from '@tanstack/svelte-table';
	import { watch } from 'runed';
	import type { Snippet } from 'svelte';
	import { features, type DataTableFeatures } from './features';

	interface Props {
		data: TData[];
		columns: ColumnDef<DataTableFeatures, TData>[];
		getRowId: (row: TData) => string;
		getRowLabel: (row: TData) => string;
		getRowClass?: (row: TData) => string | undefined;
		globalFilterFn?: FilterFn<DataTableFeatures, TData>;
		initialSorting?: SortingState;
		sorting?: SortingState;
		searchText?: string;
		pageOffset?: number;
		pageSize?: number;
		serverRowCount?: number;
		pageResetKey?: unknown;
		initialPageSize?: number;
		pageSizeOptions?: number[];
		selection?: RowSelectionState;
		selectable?: boolean;
		selectionBlock?: (row: TData) => string;
		loading?: boolean;
		searchPlaceholder?: string;
		fullWidthSearch?: boolean;
		emptyMessage?: string;
		heading?: Snippet;
		filters?: Snippet;
		preserveHeightOnFilter?: boolean;
		columnWidths?: Record<string, string>;
		columnClasses?: Record<string, string>;
		toolbar?: Snippet;
		toolbarRow?: boolean;
		mobileRow?: Snippet<[Row<DataTableFeatures, TData>]>;
		view?: 'table' | 'grid';
		gridItem?: Snippet<[Row<DataTableFeatures, TData>]>;
	}

	let {
		data,
		columns,
		getRowId,
		getRowLabel,
		getRowClass,
		globalFilterFn,
		initialSorting = [],
		sorting = $bindable([]),
		searchText = $bindable(''),
		pageOffset = $bindable(0),
		pageSize = $bindable(0),
		serverRowCount,
		pageResetKey,
		initialPageSize = 10,
		pageSizeOptions = [10, 20, 50],
		selection = $bindable({}),
		selectable = false,
		selectionBlock,
		loading = false,
		searchPlaceholder = 'Search…',
		fullWidthSearch = false,
		emptyMessage = 'No data yet.',
		heading,
		filters,
		preserveHeightOnFilter = false,
		columnWidths,
		columnClasses,
		toolbar,
		toolbarRow = false,
		mobileRow,
		view = 'table',
		gridItem
	}: Props = $props();
	let grid = $derived(view === 'grid' && Boolean(gridItem));

	// svelte-ignore state_referenced_locally
	const table = createTable({
		features,
		get data() {
			return data;
		},
		get columns() {
			return columns;
		},
		get getRowId() {
			return getRowId;
		},
		get enableRowSelection() {
			const block = selectionBlock;
			return selectable && block
				? (row: Row<DataTableFeatures, TData>) => !block(row.original)
				: selectable;
		},
		get globalFilterFn() {
			return globalFilterFn ?? 'includesString';
		},
		get manualPagination() {
			return serverRowCount !== undefined;
		},
		get manualSorting() {
			return serverRowCount !== undefined;
		},
		get manualFiltering() {
			return serverRowCount !== undefined;
		},
		get rowCount() {
			return serverRowCount;
		},
		autoResetPageIndex: false,
		initialState: {
			sorting: initialSorting,
			pagination: { pageIndex: 0, pageSize: initialPageSize }
		},
		state: {
			get rowSelection() {
				return selection;
			}
		},
		onRowSelectionChange: (updater) => {
			selection = typeof updater === 'function' ? updater(selection) : updater;
		}
	});

	let pagination = $derived(table.atoms.pagination.get());
	let query = $derived(String(table.atoms.globalFilter.get() ?? ''));

	$effect(() => {
		sorting = table.atoms.sorting.get();
		searchText = query;
		pageOffset = pagination.pageIndex * pagination.pageSize;
		pageSize = pagination.pageSize;
	});
	watch([() => table.atoms.sorting.get(), () => pageResetKey], () => table.setPageIndex(0), {
		lazy: true
	});
	let filteredCount = $derived(serverRowCount ?? table.getFilteredRowModel().rows.length);
	$effect(() => {
		const last = Math.max(0, Math.ceil(filteredCount / pagination.pageSize) - 1);
		if (pagination.pageIndex > last) table.setPageIndex(last);
	});
	let pageRows = $derived(table.getRowModel().rows);
	let sortableColumns = $derived(table.getAllColumns().filter((column) => column.getCanSort()));
	let message = $derived(loading ? 'Loading…' : query ? 'No matching results.' : emptyMessage);
	let element = $state<HTMLDivElement>();
	let unfilteredHeight = $state<number>();

	function search(value: string) {
		if (preserveHeightOnFilter && !query && value) {
			unfilteredHeight = element?.getBoundingClientRect().height;
		}
		if (!value) unfilteredHeight = undefined;
		table.setGlobalFilter(value);
		if (serverRowCount === undefined) table.setPageIndex(0);
	}

	$effect(() => {
		const available = new Set(data.filter((row) => !selectionBlock?.(row)).map(getRowId));
		const selected = Object.entries(selection).filter(
			([id, checked]) => selectable && checked && available.has(id)
		);
		if (selected.length !== Object.keys(selection).length) selection = Object.fromEntries(selected);
	});
</script>

{#snippet rowCheckbox(row: Row<DataTableFeatures, TData>, className?: string)}
	{@const blocked = selectionBlock?.(row.original) ?? ''}
	{#if blocked}
		<Tooltip.Root delayDuration={500}>
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<button
						{...props}
						type="button"
						aria-disabled="true"
						aria-label="{getRowLabel(row.original)} can’t be selected: {blocked}"
						class="border-input dark:bg-input/30 shadow-xs focus-visible:border-ring focus-visible:ring-ring/50 flex size-4 shrink-0 cursor-help rounded-lg border opacity-50 outline-none focus-visible:ring-[3px] {className ??
							''}"
					></button>
				{/snippet}
			</Tooltip.Trigger>
			<Tooltip.Content side="right" sideOffset={6} class="max-w-64 text-left text-pretty">
				{blocked}
			</Tooltip.Content>
		</Tooltip.Root>
	{:else}
		<Checkbox
			class={className}
			checked={row.getIsSelected()}
			onCheckedChange={(checked) => row.toggleSelected(checked)}
			disabled={loading}
			aria-label="Select {getRowLabel(row.original)}"
		/>
	{/if}
{/snippet}

<div
	bind:this={element}
	class="@container min-w-0"
	style:min-height={preserveHeightOnFilter && query && unfilteredHeight
		? `${unfilteredHeight}px`
		: undefined}
	aria-busy={loading}
>
	<div class="mb-3 flex flex-wrap items-center gap-2">
		{#if heading}
			<div class="w-full min-w-0 @xl:w-auto">{@render heading()}</div>
		{/if}
		<div
			class="relative min-w-40 flex-1 {fullWidthSearch ? '' : '@2xl:max-w-sm'}"
			class:ml-auto={!!heading}
		>
			<span
				class="icon-[lucide--search] text-muted-foreground pointer-events-none absolute top-2.5 left-3 size-4"
				aria-hidden="true"
			></span>
			<Input
				type="search"
				class="pl-9"
				placeholder={searchPlaceholder}
				aria-label={searchPlaceholder}
				value={query}
				oninput={(event) => search(event.currentTarget.value)}
			/>
		</div>
		{#if mobileRow || grid}
			<div class={grid ? undefined : '@2xl:hidden'}>
				<DropdownMenu.Root>
					<DropdownMenu.Trigger>
						{#snippet child({ props })}
							<Button variant="outline" size="sm" class="h-9 gap-2" {...props}>
								<span class="icon-[lucide--arrow-up-down] size-4" aria-hidden="true"></span>
								Sort
							</Button>
						{/snippet}
					</DropdownMenu.Trigger>
					<DropdownMenu.Content align="end">
						<DropdownMenu.Label>Sort By</DropdownMenu.Label>
						{#each sortableColumns as column (column.id)}
							<DropdownMenu.Item
								onclick={() => column.toggleSorting(column.getIsSorted() === 'asc')}
							>
								{typeof column.columnDef.header === 'string' ? column.columnDef.header : column.id}
								{#if column.getIsSorted()}
									<span
										class="ml-auto size-4 {column.getIsSorted() === 'asc'
											? 'icon-[lucide--arrow-up]'
											: 'icon-[lucide--arrow-down]'}"
										aria-hidden="true"
									></span>
								{/if}
							</DropdownMenu.Item>
						{/each}
					</DropdownMenu.Content>
				</DropdownMenu.Root>
			</div>
		{/if}
		{#if grid && selectable}
			<label
				class="{buttonVariants({
					variant: 'outline',
					size: 'sm'
				})} h-9 cursor-pointer gap-2 has-disabled:cursor-not-allowed has-disabled:opacity-50"
			>
				<Checkbox
					checked={table.getIsAllPageRowsSelected()}
					indeterminate={table.getIsSomePageRowsSelected() && !table.getIsAllPageRowsSelected()}
					onCheckedChange={(checked) => table.toggleAllPageRowsSelected(checked)}
					disabled={loading || pageRows.length === 0}
				/>
				Select Page
			</label>
		{/if}
		{#if toolbar}
			<div
				class="ml-auto flex flex-wrap items-center justify-end gap-2 {toolbarRow
					? 'w-full @4xl:w-auto'
					: ''}"
			>
				{@render toolbar()}
			</div>
		{/if}
	</div>
	{#if filters}
		<div class="mb-3 min-w-0">{@render filters()}</div>
	{/if}

	{#if grid && gridItem}
		{#if pageRows.length > 0}
			<ul class="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-3">
				{#each pageRows as row (row.id)}
					<li
						class="bg-card data-[state=selected]:bg-zen-highlight data-[state=selected]:border-zen-highlight-foreground/40 relative min-w-0 rounded-lg border {getRowClass?.(
							row.original
						) ?? ''}"
						data-state={row.getIsSelected() ? 'selected' : undefined}
					>
						{#if selectable}
							<div class="absolute top-2.5 left-2.5 z-10 flex">{@render rowCheckbox(row)}</div>
						{/if}
						{@render gridItem(row)}
					</li>
				{/each}
			</ul>
		{:else}
			<div
				class="bg-card text-muted-foreground flex h-32 items-center justify-center rounded-lg border px-4 text-center text-sm"
			>
				{message}
			</div>
		{/if}
	{:else}
		<div class="bg-card overflow-hidden rounded-lg border {mobileRow ? 'hidden @2xl:block' : ''}">
			<Table.Root class={columnWidths ? 'table-fixed' : undefined}>
				{#if columnWidths}
					<colgroup>
						{#if selectable}<col class="w-11" />{/if}
						{#each table.getAllLeafColumns() as column (column.id)}
							<col style:width={columnWidths[column.id]} />
						{/each}
					</colgroup>
				{/if}
				<Table.Header>
					{#each table.getHeaderGroups() as group (group.id)}
						<Table.Row>
							{#if selectable}
								<Table.Head class="w-11 pl-4">
									<Checkbox
										checked={table.getIsAllPageRowsSelected()}
										indeterminate={table.getIsSomePageRowsSelected() &&
											!table.getIsAllPageRowsSelected()}
										onCheckedChange={(checked) => table.toggleAllPageRowsSelected(checked)}
										disabled={loading || pageRows.length === 0}
										aria-label="Select all rows on this page"
									/>
								</Table.Head>
							{/if}
							{#each group.headers as header (header.id)}
								<Table.Head
									class={columnClasses?.[header.column.id]}
									colspan={header.colSpan}
									aria-sort={header.column.getCanSort()
										? header.column.getIsSorted() === 'asc'
											? 'ascending'
											: header.column.getIsSorted() === 'desc'
												? 'descending'
												: 'none'
										: undefined}
								>
									{#if !header.isPlaceholder}
										{#if header.column.getCanSort()}
											<Button
												variant="ghost"
												size="sm"
												class="-ml-2 h-8 gap-2 px-2"
												onclick={header.column.getToggleSortingHandler()}
											>
												<FlexRender {header} />
												<span
													class="size-3.5 {header.column.getIsSorted() === 'asc'
														? 'icon-[lucide--arrow-up]'
														: header.column.getIsSorted() === 'desc'
															? 'icon-[lucide--arrow-down]'
															: 'icon-[lucide--arrow-up-down]'}"
													aria-hidden="true"
												></span>
											</Button>
										{:else}
											<FlexRender {header} />
										{/if}
									{/if}
								</Table.Head>
							{/each}
						</Table.Row>
					{/each}
				</Table.Header>
				<Table.Body>
					{#each pageRows as row (row.id)}
						<Table.Row
							data-state={row.getIsSelected() ? 'selected' : undefined}
							class="data-[state=selected]:bg-zen-highlight {getRowClass?.(row.original) ?? ''}"
						>
							{#if selectable}
								<Table.Cell class="pl-4">{@render rowCheckbox(row)}</Table.Cell>
							{/if}
							{#each row.getAllCells() as cell (cell.id)}
								<Table.Cell
									class="py-3 whitespace-normal break-words {columnClasses?.[cell.column.id] ?? ''}"
								>
									<FlexRender {cell} />
								</Table.Cell>
							{/each}
						</Table.Row>
					{:else}
						<Table.Row
							><Table.Cell colspan={columns.length + Number(selectable)} class="h-32 text-center"
								>{message}</Table.Cell
							></Table.Row
						>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>

		{#if mobileRow}
			<div class="bg-card overflow-hidden rounded-lg border @2xl:hidden">
				{#if selectable && pageRows.length > 0}
					<label class="text-muted-foreground flex items-center gap-3 border-b px-4 py-3 text-xs">
						<Checkbox
							checked={table.getIsAllPageRowsSelected()}
							indeterminate={table.getIsSomePageRowsSelected() && !table.getIsAllPageRowsSelected()}
							onCheckedChange={(checked) => table.toggleAllPageRowsSelected(checked)}
							disabled={loading}
						/>
						Select Page
					</label>
				{/if}
				<ul class="divide-y">
					{#each pageRows as row (row.id)}
						<li
							class="flex items-start gap-3 p-4 data-[state=selected]:bg-zen-highlight {getRowClass?.(
								row.original
							) ?? ''}"
							data-state={row.getIsSelected() ? 'selected' : undefined}
						>
							{#if selectable}
								{@render rowCheckbox(row, 'mt-1')}
							{/if}
							<div class="min-w-0 flex-1">{@render mobileRow(row)}</div>
						</li>
					{:else}
						<li class="text-muted-foreground px-4 py-10 text-center text-sm">{message}</li>
					{/each}
				</ul>
			</div>
		{/if}
	{/if}

	<div class="text-muted-foreground mt-3 flex flex-wrap items-center justify-between gap-3 text-xs">
		<p aria-live="polite">
			{grid ? 'Items' : 'Rows'}
			{filteredCount ? pagination.pageIndex * pagination.pageSize + 1 : 0}–{Math.min(
				(pagination.pageIndex + 1) * pagination.pageSize,
				filteredCount
			)} of {filteredCount}
		</p>
		<div class="flex flex-wrap items-center gap-2">
			<label class="flex items-center gap-2">
				{grid ? 'Items' : 'Rows'} Per Page
				<select
					class="border-input bg-background h-9 rounded-md border px-2 text-sm"
					value={pagination.pageSize}
					onchange={(event) => table.setPageSize(Number(event.currentTarget.value))}
				>
					{#each pageSizeOptions as size (size)}<option value={size}>{size}</option>{/each}
				</select>
			</label>
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				onclick={() => table.previousPage()}
				disabled={!table.getCanPreviousPage()}
				aria-label="Previous page"
				><span class="icon-[lucide--chevron-left] size-4" aria-hidden="true"></span></Button
			>
			<span class="tabular-nums"
				>{pagination.pageIndex + 1} / {Math.max(1, table.getPageCount())}</span
			>
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				onclick={() => table.nextPage()}
				disabled={!table.getCanNextPage()}
				aria-label="Next page"
				><span class="icon-[lucide--chevron-right] size-4" aria-hidden="true"></span></Button
			>
		</div>
	</div>
</div>
