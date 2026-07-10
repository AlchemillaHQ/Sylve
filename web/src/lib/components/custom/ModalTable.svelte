<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { deepEqual } from 'fast-equals';
	import { watch } from 'runed';
	import { onMount } from 'svelte';
	import {
		TabulatorFull as Tabulator,
		type ColumnDefinition,
		type Sorter
	} from 'tabulator-tables';

	interface Props {
		rows: Record<string, unknown>[];
		columns: ColumnDefinition[];
		pageSize?: number;
		pageSizeOptions?: number[];
		placeholder?: string;
		initialSort?: Sorter[];
	}

	let {
		rows,
		columns,
		pageSize = 10,
		pageSizeOptions = [10, 20, 50],
		placeholder = 'No data available',
		initialSort = []
	}: Props = $props();
	let tableComponent: HTMLDivElement | null = null;
	let table: Tabulator | null = null;
	let tableReady = false;
	let pendingRows = rows;

	async function replaceRows(nextRows: Record<string, unknown>[]) {
		const currentTable = table;
		if (!currentTable || !tableReady) return;
		try {
			await currentTable.replaceData(nextRows);
		} catch (error) {
			if (tableReady) console.error('Modal table data update failed', error);
		}
	}

	onMount(() => {
		if (!tableComponent) return;
		const initialRows = rows;
		table = new Tabulator(tableComponent, {
			data: rows,
			columns,
			height: '100%',
			layout: 'fitColumns',
			reactiveData: false,
			selectableRows: false,
			pagination: true,
			paginationMode: 'local',
			paginationSize: pageSize,
			paginationSizeSelector: pageSizeOptions,
			paginationCounter: 'rows',
			initialSort,
			placeholder,
			debugInvalidOptions: false
		});
		table.on('tableBuilt', () => {
			tableReady = true;
			if (pendingRows !== initialRows) void replaceRows(pendingRows);
		});
		return () => {
			tableReady = false;
			table?.destroy();
			table = null;
		};
	});

	watch(
		() => rows,
		(nextRows) => {
			if (deepEqual(nextRows, pendingRows)) return;
			pendingRows = nextRows;
			void replaceRows(nextRows);
		}
	);
</script>

<div
	bind:this={tableComponent}
	class="s-modal-table-container flex h-full min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
></div>

<style>
	:global(.s-modal-table-container.tabulator) {
		display: flex;
		min-height: 0;
		width: 100%;
		flex: 1;
		flex-direction: column;
		border: 1px solid var(--border);
		border-radius: calc(var(--radius) + 2px);
		background: var(--background);
	}

	:global(.s-modal-table-container.tabulator > .tabulator-tableholder) {
		min-height: 0;
		flex: 1;
	}

	:global(.s-modal-table-container.tabulator > .tabulator-header) {
		border-bottom: 1px solid var(--border);
	}

	:global(.s-modal-table-container.tabulator .tabulator-header),
	:global(.s-modal-table-container.tabulator .tabulator-header-contents),
	:global(.s-modal-table-container.tabulator .tabulator-header .tabulator-col),
	:global(.s-modal-table-container.tabulator .tabulator-header .tabulator-col-content) {
		background: var(--muted);
	}

	:global(.s-modal-table-container.tabulator .tabulator-header .tabulator-col-content),
	:global(.s-modal-table-container.tabulator .tabulator-row .tabulator-cell) {
		padding: 0.625rem 0.25rem;
	}

	:global(.s-modal-table-container.tabulator .tabulator-tableholder .tabulator-table) {
		background: var(--background);
	}

	:global(.s-modal-table-container.tabulator .tabulator-row:hover) {
		background: var(--muted);
	}

	:global(.s-modal-table-container.tabulator > .tabulator-footer) {
		border-top: 1px solid var(--border);
	}

	:global(.s-modal-table-container.tabulator .tabulator-footer .tabulator-footer-contents) {
		flex-wrap: wrap;
		min-height: 3rem;
		gap: 1rem;
		padding: 0.5rem 0.75rem;
	}

	:global(.s-modal-table-container.tabulator .tabulator-footer .tabulator-page-counter) {
		color: var(--muted-foreground);
		font-size: 0.8125rem;
		font-weight: 500;
		white-space: nowrap;
	}

	:global(.s-modal-table-container.tabulator .tabulator-footer .tabulator-paginator) {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: flex-end;
		gap: 0.375rem;
		color: var(--foreground);
		font-size: 0.8125rem;
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			label
	) {
		color: var(--muted-foreground);
		font-weight: 500;
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page-size
	) {
		height: 2rem;
		margin: 0 0.25rem 0 0;
		padding: 0 0.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		outline: none;
		background: var(--background);
		color: var(--foreground);
		color-scheme: light;
		font-weight: 500;
	}

	:global(
		.dark
			.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page-size
	) {
		color-scheme: dark;
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page-size:focus
	) {
		border-color: var(--ring);
		box-shadow: 0 0 0 2px color-mix(in oklch, var(--ring) 35%, transparent);
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page-size
			option
	) {
		background: var(--popover);
		color: var(--popover-foreground);
	}

	:global(.s-modal-table-container.tabulator .tabulator-footer .tabulator-pages) {
		display: inline-flex;
		gap: 0.25rem;
		margin: 0;
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page
	) {
		height: 2rem;
		min-width: 2rem;
		margin: 0;
		padding: 0 0.625rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: transparent;
		color: var(--foreground);
		font-weight: 500;
		transition:
			background-color 150ms ease,
			border-color 150ms ease,
			color 150ms ease;
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page:not(:disabled):hover
	) {
		border-color: var(--ring);
		background: var(--accent);
		color: var(--accent-foreground);
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page.active
	),
	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page.active:hover
	) {
		border-color: var(--foreground);
		background: var(--foreground);
		color: var(--background);
	}

	:global(
		.s-modal-table-container.tabulator
			.tabulator-footer
			.tabulator-footer-contents
			.tabulator-paginator
			.tabulator-page:disabled
	) {
		background: transparent;
		color: var(--muted-foreground);
		cursor: default;
		opacity: 0.4;
	}
</style>
