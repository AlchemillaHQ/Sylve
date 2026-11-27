<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getSambaAuditLogs } from '$lib/api/samba/audit';
	import { getSambaSharesResult } from '$lib/api/samba/share';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import type { SambaAuditLog, SambaAuditLogQuery } from '$lib/types/samba/audit';
	import type { SambaShare } from '$lib/types/samba/shares';
	import { cn, sleep } from '$lib/utils.js';
	import { APIRequestError, isAPIResponse } from '$lib/utils/http';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type SortingState
	} from '@tanstack/svelte-table';
	import { format } from 'date-fns';
	import { Debounced, resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { AUDIT_OPERATIONS, auditOperationLabel } from './samba';

	let { hostname }: { hostname: string } = $props();

	type Range = 'any' | '1h' | '24h' | '7d' | '30d' | 'custom';
	const RANGES: { value: Range; label: string; hours?: number }[] = [
		{ value: 'any', label: 'Any Time' },
		{ value: '1h', label: 'Last Hour', hours: 1 },
		{ value: '24h', label: 'Last 24 Hours', hours: 24 },
		{ value: '7d', label: 'Last 7 Days', hours: 24 * 7 },
		{ value: '30d', label: 'Last 30 Days', hours: 24 * 30 },
		{ value: 'custom', label: 'Custom Range' }
	];
	const SORT_FIELDS: Record<string, string> = {
		time: 'created_at',
		user: 'user',
		share: 'share',
		action: 'action',
		path: 'path'
	};

	const id = $props.id();
	// svelte-ignore state_referenced_locally
	const host = hostname;
	const lifetime = new AbortController();
	const firstLoadShown = sleep(500);

	const INITIAL_SORTING: SortingState = [{ id: 'time', desc: true }];
	let sorting = $state<SortingState>(INITIAL_SORTING);
	let searchText = $state('');
	let pageOffset = $state(0);
	let pageSize = $state(100);
	let share = $state('all');
	let actions = $state<string[]>([]);
	let user = $state('');
	let range = $state<Range>('any');
	let from = $state('');
	let to = $state('');
	let total = $state(0);
	let failed = $state(false);
	let shares = $state<SambaShare[]>([]);
	const search = new Debounced(() => searchText, 300);
	const userFilter = new Debounced(() => user, 300);

	let rangeProblem = $derived(
		range === 'custom' && from && to && new Date(from) > new Date(to)
			? 'The start must be before the end'
			: ''
	);
	let filtered = $derived(
		share !== 'all' || actions.length > 0 || user.trim() !== '' || range !== 'any'
	);
	let filterKey = $derived(
		JSON.stringify([share, actions, userFilter.current, range, from, to, search.current])
	);
	let auditing = $derived(shares.some((entry) => entry.auditEnabled && entry.enabled));
	let mountpoints = $derived(
		Object.fromEntries(shares.map((entry) => [entry.name.toLowerCase(), entry.mountpoint]))
	);
	let customRange = $derived(range === 'custom' && !rangeProblem);
	let query = $derived<SambaAuditLogQuery & { range: Range }>({
		page: Math.floor(pageOffset / pageSize) + 1,
		size: pageSize,
		sortField: SORT_FIELDS[sorting[0]?.id ?? 'time'] ?? 'created_at',
		sortDir: sorting[0] && !sorting[0].desc ? 'asc' : 'desc',
		share: share === 'all' ? '' : share,
		user: userFilter.current,
		actions: [...actions],
		search: search.current,
		range,
		from: customRange && from ? new Date(from).toISOString() : undefined,
		to: customRange && to ? new Date(to).toISOString() : undefined
	});

	const logs = resource(
		() => query,
		async (current, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const hours = RANGES.find((entry) => entry.value === current.range)?.hours;
			const request: SambaAuditLogQuery = hours
				? { ...current, from: new Date(Date.now() - hours * 3_600_000).toISOString() }
				: current;
			const result = await getSambaAuditLogs(request, { hostname: host, signal: requestSignal });
			await firstLoadShown;
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) {
				failed = true;
				throw new APIRequestError(result);
			}
			failed = false;
			total = result.total;
			return result.data;
		},
		{ initialValue: [] as SambaAuditLog[], debounce: 50 }
	);

	async function loadShares() {
		const result = await getSambaSharesResult({ hostname: host, signal: lifetime.signal }).catch(
			() => null
		);
		if (!lifetime.signal.aborted && result && !isAPIResponse(result)) {
			shares = [...result].sort((a, b) => a.name.localeCompare(b.name));
		}
	}

	function refresh() {
		if (!logs.loading) void logs.refetch();
		void loadShares();
	}

	function clearFilters() {
		share = 'all';
		actions = [];
		user = '';
		range = 'any';
		from = '';
		to = '';
	}

	const clearClasses = 'shrink-0 gap-2 @3xl:w-auto @3xl:px-3';

	function toggleAction(value: string, checked: boolean) {
		actions = checked ? [...actions, value] : actions.filter((entry) => entry !== value);
	}

	function shortTime(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		return format(
			date,
			date.getFullYear() === new Date().getFullYear() ? 'MMM d, HH:mm:ss' : 'MMM d, yyyy HH:mm:ss'
		);
	}

	function longTime(value: string): string {
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
	}

	function relativePath(entry: SambaAuditLog, path: string): string {
		const root = mountpoints[entry.share.toLowerCase()];
		if (!root || !root.startsWith('/') || !path.startsWith(root)) return path;
		const rest = path.slice(root.length);
		if (rest === '') return '/';
		return rest.startsWith('/') ? rest : path;
	}

	void loadShares();
	const interval = useInterval(() => (storage.idle ? 120_000 : 30_000), {
		callback: () => {
			if (storage.visible && !logs.loading) void logs.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !logs.loading) void logs.refetch();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, SambaAuditLog>();
	const columns = helper.columns([
		helper.accessor('createdAt', {
			id: 'time',
			header: 'Time',
			sortDescFirst: true,
			cell: ({ row }) => renderSnippet(timeCell, row.original)
		}),
		helper.accessor('user', {
			id: 'user',
			header: 'User',
			cell: ({ row }) => renderSnippet(userCell, row.original)
		}),
		helper.accessor('share', {
			id: 'share',
			header: 'Share',
			cell: ({ row }) => renderSnippet(shareCell, row.original)
		}),
		helper.accessor('action', {
			id: 'action',
			header: 'Action',
			cell: ({ row }) => renderSnippet(actionCell, row.original)
		}),
		helper.accessor('path', {
			id: 'path',
			header: 'Path',
			cell: ({ row }) => renderSnippet(pathCell, row.original)
		})
	]);
</script>

{#snippet timeCell(entry: SambaAuditLog)}
	<time datetime={entry.createdAt} title={longTime(entry.createdAt)} class="whitespace-nowrap">
		{shortTime(entry.createdAt)}
	</time>
{/snippet}

{#snippet userCell(entry: SambaAuditLog)}
	<div class="grid grid-cols-[minmax(0,1fr)]">
		<p class="truncate">{entry.user || '—'}</p>
		{#if entry.client || entry.ip}
			<p class="text-muted-foreground truncate text-xs" title={entry.ip}>
				{entry.client && entry.client !== entry.ip ? `${entry.client} · ${entry.ip}` : entry.ip}
			</p>
		{/if}
	</div>
{/snippet}

{#snippet shareCell(entry: SambaAuditLog)}
	<div class="grid grid-cols-[minmax(0,1fr)]">
		<span class="truncate">{entry.share || '—'}</span>
	</div>
{/snippet}

{#snippet actionCell(entry: SambaAuditLog)}
	{@const operation = AUDIT_OPERATIONS.find((item) => item.value === entry.action)}
	<span class="flex flex-wrap items-center gap-x-2 gap-y-1">
		<span class="flex items-center gap-1.5 whitespace-nowrap">
			<span
				class="{operation?.icon ?? 'icon-[lucide--activity]'} text-muted-foreground size-4 shrink-0"
				aria-hidden="true"
			></span>
			{auditOperationLabel(entry.action)}
		</span>
		{#if entry.occurrences > 1}
			<span
				class="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs tabular-nums"
				title="Happened {entry.occurrences} times in a row">×{entry.occurrences}</span
			>
		{/if}
		{#if entry.result && entry.result !== 'ok'}
			<span class="text-destructive text-xs font-medium">Failed</span>
		{/if}
	</span>
{/snippet}

{#snippet truncatedPath(path: string)}
	{@const split = path.lastIndexOf('/') + 1}
	<span class="flex min-w-0">
		{#if split > 0 && split < path.length}
			<span class="min-w-0 truncate">{path.slice(0, split)}</span>
			<span class="max-w-[70%] shrink-0 truncate">{path.slice(split)}</span>
		{:else}
			<span class="min-w-0 truncate">{path}</span>
		{/if}
	</span>
{/snippet}

{#snippet pathCell(entry: SambaAuditLog)}
	{#if entry.path}
		<div class="grid grid-cols-[minmax(0,1fr)]">
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring flex min-w-0 cursor-default flex-col rounded-sm text-left font-mono text-xs focus-visible:ring-2 focus-visible:outline-none"
					aria-label={entry.target ? `${entry.path} → ${entry.target}` : entry.path}
				>
					{@render truncatedPath(relativePath(entry, entry.path))}
					{#if entry.target}
						<span class="text-muted-foreground flex min-w-0 gap-1">
							<span class="shrink-0" aria-hidden="true">→</span>
							{@render truncatedPath(relativePath(entry, entry.target))}
						</span>
					{/if}
				</Tooltip.Trigger>
				<Tooltip.Content side="top" align="start" sideOffset={6} class="max-w-96 text-left">
					<p class="break-all">{entry.path}</p>
					{#if entry.target}
						<p class="mt-0.5 break-all opacity-70">→ {entry.target}</p>
					{/if}
				</Tooltip.Content>
			</Tooltip.Root>
		</div>
	{:else}
		<span class="text-muted-foreground">—</span>
	{/if}
{/snippet}

{#snippet filters()}
	<div class="grid grid-cols-2 gap-2 @3xl:grid-cols-[repeat(4,minmax(0,1fr))_auto]">
		<Select.Root type="single" bind:value={share}>
			<Select.Trigger
				class="bg-background h-9 w-full dark:hover:bg-input/30"
				aria-label="Filter by share"
			>
				<span class="truncate">{share === 'all' ? 'All Shares' : share}</span>
			</Select.Trigger>
			<Select.Content>
				<Select.Item value="all" label="All Shares" />
				{#each shares as entry (entry.id)}
					<Select.Item value={entry.name} label={entry.name} />
				{/each}
			</Select.Content>
		</Select.Root>
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="outline"
						class="bg-background h-9 w-full justify-between gap-2 font-normal dark:hover:bg-input/30"
						aria-label="Filter by action"
					>
						<span class="truncate">
							{actions.length === 0
								? 'All Actions'
								: actions.length === 1
									? auditOperationLabel(actions[0])
									: `${actions.length} Actions`}
						</span>
						<span class="icon-[lucide--chevron-down] size-4 shrink-0 opacity-50" aria-hidden="true"
						></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="start" class="w-52">
				{#each AUDIT_OPERATIONS as operation (operation.value)}
					<DropdownMenu.CheckboxItem
						checked={actions.includes(operation.value)}
						onCheckedChange={(checked) => toggleAction(operation.value, checked)}
						closeOnSelect={false}
					>
						{operation.label}
					</DropdownMenu.CheckboxItem>
				{/each}
				{#if actions.length}
					<DropdownMenu.Separator />
					<DropdownMenu.Item class="cursor-pointer" onSelect={() => (actions = [])}>
						Show All Actions
					</DropdownMenu.Item>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
		<Select.Root type="single" bind:value={range}>
			<Select.Trigger
				class="bg-background h-9 w-full dark:hover:bg-input/30"
				aria-label="Filter by time"
			>
				<span class="truncate">{RANGES.find((entry) => entry.value === range)?.label}</span>
			</Select.Trigger>
			<Select.Content>
				{#each RANGES as entry (entry.value)}
					<Select.Item value={entry.value} label={entry.label} />
				{/each}
			</Select.Content>
		</Select.Root>
		<div class="flex min-w-0 gap-2 @3xl:contents">
			<Input
				class="h-9 w-full min-w-0"
				placeholder="User"
				aria-label="Filter by user"
				autocomplete="off"
				spellcheck={false}
				bind:value={user}
			/>
			{@render clearButton()}
		</div>
		{#if range === 'custom'}
			<div class="col-span-full flex flex-wrap items-center gap-2">
				<label class="flex items-center gap-2 text-sm" for="{id}-from">
					<span class="text-muted-foreground">From</span>
					<Input
						id="{id}-from"
						type="datetime-local"
						step="1"
						class="h-9 w-auto"
						bind:value={from}
						aria-invalid={rangeProblem ? true : undefined}
					/>
				</label>
				<label class="flex items-center gap-2 text-sm" for="{id}-to">
					<span class="text-muted-foreground">To</span>
					<Input
						id="{id}-to"
						type="datetime-local"
						step="1"
						class="h-9 w-auto"
						bind:value={to}
						aria-invalid={rangeProblem ? true : undefined}
					/>
				</label>
				{#if rangeProblem}
					<span class="text-destructive text-xs" role="alert">({rangeProblem})</span>
				{/if}
			</div>
		{/if}
	</div>
{/snippet}

{#snippet clearButton()}
	{#if filtered}
		<Button variant="outline" size="icon" class={clearClasses} onclick={clearFilters}>
			<span class="icon-[lucide--filter-x] size-4" aria-hidden="true"></span>
			<span class="sr-only @3xl:not-sr-only">Clear Filters</span>
		</Button>
	{:else}
		<Tooltip.Root delayDuration={500}>
			<Tooltip.Trigger
				type="button"
				aria-disabled="true"
				class={cn(
					buttonVariants({ variant: 'outline', size: 'icon' }),
					clearClasses,
					'cursor-help aria-disabled:pointer-events-auto'
				)}
			>
				<span class="icon-[lucide--filter-x] size-4" aria-hidden="true"></span>
				<span class="sr-only @3xl:not-sr-only">Clear Filters</span>
				<span class="sr-only">: No filters are set</span>
			</Tooltip.Trigger>
			<Tooltip.Content side="bottom" align="end" sideOffset={6} class="max-w-64 text-left">
				No filters are set
			</Tooltip.Content>
		</Tooltip.Root>
	{/if}
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, SambaAuditLog>)}
	{@const entry = row.original}
	<div class="mb-2 flex min-w-0 items-center justify-between gap-3">
		{@render actionCell(entry)}
		<span class="text-muted-foreground shrink-0 text-xs">{@render timeCell(entry)}</span>
	</div>
	<p class="text-muted-foreground mb-2 truncate text-xs">
		{entry.user || 'Unknown user'}{entry.share ? ` on ${entry.share}` : ''}{entry.ip
			? ` · ${entry.ip}`
			: ''}
	</p>
	{@render pathCell(entry)}
{/snippet}

<svelte:head><title>Samba Audit Log · {host}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[material-symbols--smb-share] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Samba</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Audit Log</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<Button
			variant="outline"
			size="icon"
			class="size-9"
			disabled={logs.loading}
			onclick={refresh}
			aria-label="Refresh audit log"
		>
			<span
				class="icon-[lucide--refresh-cw] size-4"
				class:animate-spin={logs.loading}
				aria-hidden="true"
			></span>
		</Button>
	</header>
	{#if failed}
		<div
			class="bg-card mb-4 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span
				class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
				aria-hidden="true"
			></span>
			<p class="min-w-0 flex-1">
				{logs.current.length > 0
					? 'The audit log could not be refreshed. Showing the last loaded entries.'
					: 'The audit log could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={logs.loading} onclick={refresh}>
				Try Again
			</Button>
		</div>
	{:else if shares.length > 0 && !auditing}
		<div
			class="bg-card text-muted-foreground mb-4 flex items-center gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span class="icon-[lucide--info] size-4 shrink-0" aria-hidden="true"></span>
			<p class="min-w-0 flex-1">
				No share is recording activity. Turn on Audit Logging in a share’s Audit tab.
			</p>
		</div>
	{/if}
	<DataTable
		data={logs.current}
		{columns}
		getRowId={(entry) => String(entry.id)}
		getRowLabel={(entry) => `${auditOperationLabel(entry.action)} ${entry.path}`}
		serverRowCount={total}
		bind:sorting
		bind:searchText
		bind:pageOffset
		bind:pageSize
		pageResetKey={filterKey}
		initialSorting={INITIAL_SORTING}
		initialPageSize={100}
		pageSizeOptions={[50, 100, 250, 500]}
		columnClasses={{
			time: 'w-44',
			user: 'min-w-36',
			share: 'min-w-28',
			action: 'min-w-36',
			path: 'min-w-64'
		}}
		loading={logs.loading && logs.current.length === 0}
		searchPlaceholder="Search paths, users, shares or clients…"
		emptyMessage={filtered ? 'No entries match these filters.' : 'No audit entries yet.'}
		fullWidthSearch
		{filters}
		{mobileRow}
	/>
</div>
