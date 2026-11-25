<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { pushState, replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { fileExplorerDownloadURL, getFolderListing } from '$lib/api/system/file-explorer';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { isDemoMode } from '$lib/demo/runtime';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { cn } from '$lib/utils.js';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import { PersistedState, useInterval, watch } from 'runed';
	import { onDestroy, onMount, tick, untrack, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import MenuAction from '../Disks/MenuAction.svelte';
	import Delete from './Delete.svelte';
	import Details from './Details.svelte';
	import Transfer from './Transfer.svelte';
	import Upload from './Upload.svelte';
	import {
		BATCH_LIMIT,
		browseErrorMessage,
		canDownload,
		changeBlock,
		compareEntries,
		downloadBlock,
		downloadPlan,
		downloadSelectionBlock,
		entryIcon,
		entryName,
		isFolder,
		normalizePath,
		ownerLabel,
		parentPath,
		pathSegments,
		permissionString,
		selectionBlock,
		specialLabel,
		tileDetail,
		tileTitle
	} from './explorer';
	import NameDialog from './NameDialog.svelte';

	let { hostname }: { hostname: string } = $props();

	const LIST_LIMIT = 10_000;
	const lifetime = new AbortController();
	const lastPath = new PersistedState(`sylve-zen-explorer-path:${untrack(() => hostname)}`, '/');
	const view = new PersistedState<'list' | 'grid'>('sylve-zen-explorer-view', 'list');
	const views = [
		{ value: 'list', label: 'List view', icon: 'icon-[lucide--list]' },
		{ value: 'grid', label: 'Grid view', icon: 'icon-[lucide--layout-grid]' }
	] as const;

	let path = $derived(
		normalizePath(page.state.explorerPath) ??
			normalizePath(page.url.searchParams.get('path')) ??
			normalizePath(lastPath.current) ??
			'/'
	);
	let segments = $derived(pathSegments(path));
	let entries = $state<FileNode[]>([]);
	let shownPath = $state<string | null>(null);
	let loading = $state(false);
	let failure = $state<{ path: string; message: string } | null>(null);
	let listing = $state<{
		path: string;
		query: string;
		total: number;
		matched: number;
		truncated: boolean;
	} | null>(null);
	let searchText = $state('');
	let capped = $derived(
		listing && listing.path === path && shownPath === path && listing.total > LIST_LIMIT
			? listing
			: null
	);
	let current = $derived(shownPath === path ? entries : []);
	let missing = $derived(failure?.path === path && shownPath !== path ? failure.message : '');
	let stale = $derived(failure?.path === path && shownPath === path);
	let names = $derived(new Set(current.map((entry) => entryName(entry.id))));
	let newBlock = $derived(
		shownPath === path
			? ''
			: missing
				? 'This folder couldn’t be opened.'
				: 'The folder is still loading.'
	);

	let editingPath = $state(false);
	let pathDraft = $state('');
	let pathError = $state('');
	let crumbs = $state<HTMLElement | null>(null);

	let detailsId = $state('');
	let details = $derived(detailsId ? current.find((entry) => entry.id === detailsId) : undefined);
	let nameDialog = $state<{
		session: number;
		mode: 'folder' | 'file' | 'rename';
		id?: string;
	} | null>(null);
	let renaming = $derived(
		nameDialog?.id ? current.find((entry) => entry.id === nameDialog?.id) : undefined
	);
	let dialogSession = 0;
	let selection = $state<RowSelectionState>({});
	let selected = $derived(current.filter((entry) => selection[entry.id]));
	let selectedBlock = $derived(selectionBlock(selected));
	let selectedDownloadBlock = $derived(downloadSelectionBlock(selected));
	let copyBlock = $derived(
		selected.length > BATCH_LIMIT ? `Select ${BATCH_LIMIT} items or fewer.` : ''
	);
	let deleteTargets = $state<FileNode[] | null>(null);
	let transfer = $state<{ mode: 'copy' | 'move'; items: FileNode[] } | null>(null);
	let upload = $state<{ session: number; files: File[] } | null>(null);
	let dragging = $state(false);
	let dragDepth = 0;

	let request = 0;
	let inflight: AbortController | null = null;

	async function load(target: string, query = '') {
		const id = ++request;
		inflight?.abort();
		const controller = new AbortController();
		inflight = controller;
		loading = true;
		try {
			const result = await getFolderListing(
				target,
				{ limit: LIST_LIMIT, query },
				hostname,
				AbortSignal.any([controller.signal, lifetime.signal])
			);
			if (id !== request) return;
			if (isAPIResponse(result)) {
				failure = { path: target, message: browseErrorMessage(result) };
				return;
			}
			entries = result.entries;
			shownPath = target;
			listing = {
				path: target,
				query,
				total: result.total,
				matched: result.matched,
				truncated: result.truncated
			};
			failure = null;
			lastPath.current = target;
		} catch (error) {
			if (id !== request || lifetime.signal.aborted || isRequestCancellation(error)) return;
			failure = { path: target, message: 'This folder could not be loaded.' };
		} finally {
			if (id === request) loading = false;
		}
	}

	function urlFor(target: string): string {
		const route = resolve('/[node]/storage/explorer', { node: hostname });
		return `${route}?${new URLSearchParams({ path: target })}`;
	}

	function open(target: string) {
		const next = normalizePath(target);
		if (!next) return;
		detailsId = '';
		if (next === path) {
			void load(next);
			return;
		}
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(urlFor(next), { explorerPath: next });
	}

	$effect(() => {
		if (!listing || listing.path !== path) return;
		const wanted = listing.total > LIST_LIMIT ? searchText.trim() : '';
		if (wanted === listing.query) return;
		const target = path;
		const timer = setTimeout(() => void load(target, wanted), 300);
		return () => clearTimeout(timer);
	});

	function refresh() {
		void load(path, listing?.path === path ? listing.query : '');
	}

	function startEditing() {
		pathDraft = path;
		pathError = '';
		editingPath = true;
	}

	function submitPath(event: SubmitEvent) {
		event.preventDefault();
		const next = normalizePath(pathDraft);
		if (!next) {
			pathError = 'Use an absolute path like /usr/local';
			return;
		}
		editingPath = false;
		open(next);
	}

	function openNameDialog(mode: 'folder' | 'file' | 'rename', id?: string) {
		detailsId = '';
		nameDialog = { session: ++dialogSession, mode, id };
	}

	function openUpload(files: File[] = []) {
		detailsId = '';
		upload = { session: ++dialogSession, files };
	}

	function draggingFiles(event: DragEvent): boolean {
		return !newBlock && !upload && (event.dataTransfer?.types.includes('Files') ?? false);
	}

	function dragEnter(event: DragEvent) {
		if (!draggingFiles(event)) return;
		event.preventDefault();
		dragDepth += 1;
		dragging = true;
	}

	function dragOver(event: DragEvent) {
		if (!draggingFiles(event)) return;
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
	}

	function dragLeave(event: DragEvent) {
		if (!dragging) return;
		event.preventDefault();
		dragDepth = Math.max(0, dragDepth - 1);
		if (dragDepth === 0) dragging = false;
	}

	function drop(event: DragEvent) {
		const accepted = draggingFiles(event);
		dragDepth = 0;
		dragging = false;
		if (!accepted) return;
		event.preventDefault();
		const items = [...(event.dataTransfer?.items ?? [])].filter((item) => item.kind === 'file');
		const folders = items.filter((item) => item.webkitGetAsEntry()?.isDirectory);
		const dropped = items
			.filter((item) => !item.webkitGetAsEntry()?.isDirectory)
			.map((item) => item.getAsFile())
			.filter((file): file is File => file !== null);
		if (folders.length > 0) {
			toast.info(
				folders.length === 1
					? 'The dropped folder was skipped. Only files can be uploaded.'
					: `${folders.length} dropped folders were skipped. Only files can be uploaded.`,
				{ position: 'bottom-center' }
			);
		}
		if (dropped.length > 0) openUpload(dropped);
	}

	function openDelete(items: FileNode[]) {
		detailsId = '';
		deleteTargets = items;
	}

	function openTransfer(mode: 'copy' | 'move', items: FileNode[]) {
		detailsId = '';
		transfer = { mode, items };
	}

	async function download(entries: FileNode[]) {
		if (isDemoMode) {
			toast.info('Downloads aren’t available in the demo', { position: 'bottom-center' });
			return;
		}
		const plan = downloadPlan(entries);
		const href = await fileExplorerDownloadURL(plan.kind, plan.ids, hostname);
		if (isAPIResponse(href)) {
			handleAPIError(href);
			toast.error(
				entries.length === 1
					? `${entryName(entries[0].id)} could not be downloaded`
					: 'The selected items could not be downloaded',
				{ position: 'bottom-center' }
			);
			return;
		}
		const link = Object.assign(document.createElement('a'), {
			href,
			download: plan.filename,
			rel: 'noopener'
		});
		document.body.append(link);
		link.click();
		link.remove();
	}

	watch(
		() => path,
		(target) => {
			selection = {};
			void load(target);
		}
	);
	watch(
		() => [path, editingPath, crumbs] as const,
		() => {
			void tick().then(() => {
				const viewport = crumbs?.querySelector<HTMLElement>('[data-slot="scroll-area-viewport"]');
				viewport?.scrollTo({ left: viewport.scrollWidth });
			});
		}
	);
	onMount(() => {
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		if (page.state.explorerPath !== path) replaceState(urlFor(path), { explorerPath: path });
	});

	const interval = useInterval(() => (storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (storage.visible && !loading) refresh();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !loading) refresh();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, FileNode>();
	const columns = helper.columns([
		helper.accessor((entry) => entryName(entry.id), {
			id: 'name',
			header: 'Name',
			sortFn: (a, b) => compareEntries(a.original, b.original),
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		}),
		helper.accessor((entry) => (canDownload(entry) ? (entry.size ?? 0) : -1), {
			id: 'size',
			header: 'Size',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(sizeCell, row.original)
		}),
		helper.accessor((entry) => entry.date.getTime(), {
			id: 'modified',
			header: 'Modified',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(dateCell, row.original.date)
		}),
		helper.accessor((entry) => ownerLabel(entry), {
			id: 'owner',
			header: 'Owner',
			sortFn: 'alphanumeric',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(mutedCell, getValue())
		}),
		helper.accessor((entry) => permissionString(entry), {
			id: 'permissions',
			header: 'Permissions',
			enableSorting: false,
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(monoCell, getValue())
		}),
		helper.display({
			id: 'actions',
			header: () => renderSnippet(actionsHeader, undefined),
			cell: ({ row }) => renderSnippet(rowActions, row.original),
			enableSorting: false,
			enableGlobalFilter: false
		})
	]);
</script>

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(entry: FileNode)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for {entryName(entry.id)}"
						{...props}
					>
						<span class="icon-[lucide--ellipsis] size-5" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content
				align="end"
				class="w-56"
				onCloseAutoFocus={(event) => {
					if (detailsId || nameDialog || deleteTargets || transfer) event.preventDefault();
				}}
			>
				{#if isFolder(entry)}
					<MenuAction
						label="Open"
						icon="icon-[lucide--folder-open]"
						onSelect={() => open(entry.id)}
					/>
					<MenuAction
						label="Download as Zip"
						icon="icon-[lucide--folder-down]"
						onSelect={() => void download([entry])}
					/>
				{:else}
					<MenuAction
						label="Download"
						icon="icon-[lucide--download]"
						blocked={downloadBlock(entry)}
						onSelect={() => void download([entry])}
					/>
				{/if}
				<MenuAction
					label="View Details"
					icon="icon-[lucide--eye]"
					onSelect={() => (detailsId = entry.id)}
				/>
				<DropdownMenu.Separator />
				<MenuAction
					label="Rename"
					icon="icon-[lucide--text-cursor-input]"
					blocked={changeBlock(entry)}
					onSelect={() => openNameDialog('rename', entry.id)}
				/>
				<MenuAction
					label="Copy To…"
					icon="icon-[lucide--copy]"
					onSelect={() => openTransfer('copy', [entry])}
				/>
				<MenuAction
					label="Move To…"
					icon="icon-[lucide--folder-input]"
					blocked={changeBlock(entry)}
					onSelect={() => openTransfer('move', [entry])}
				/>
				<DropdownMenu.Separator />
				<MenuAction
					label="Delete"
					icon="icon-[lucide--trash-2]"
					blocked={changeBlock(entry)}
					destructive
					onSelect={() => openDelete([entry])}
				/>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet markers(entry: FileNode)}
	{#if entry.broken}
		<Badge
			variant="outline"
			class="shrink-0 px-1.5 py-0 text-[10px] font-medium text-amber-600 dark:text-amber-400"
			title="Points to {entry.link}, which is missing"
		>
			Broken Link
		</Badge>
	{:else if entry.link}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="Points to {entry.link}"
		>
			Link
		</Badge>
	{/if}
	{#if entry.mount}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="{entry.mount} is mounted here"
		>
			Mount
		</Badge>
	{/if}
	{#if entry.special}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
		>
			{specialLabel(entry)}
		</Badge>
	{/if}
{/snippet}

{#snippet gridItem(row: Row<DataTableFeatures, FileNode>)}
	{@const entry = row.original}
	{@const folder = isFolder(entry)}
	<div class="absolute top-1 right-1">{@render rowActions(entry)}</div>
	<div class="flex h-full flex-col items-center gap-1.5 px-2 pt-5 pb-3 text-center">
		<button
			type="button"
			class="group focus-visible:ring-ring flex w-full min-w-0 cursor-pointer flex-col items-center gap-2 rounded-md p-1 focus-visible:ring-2 focus-visible:outline-none"
			title={tileTitle(entry)}
			aria-haspopup={folder ? undefined : 'dialog'}
			onclick={() => (folder ? open(entry.id) : (detailsId = entry.id))}
		>
			<span
				class="{entryIcon(entry)} size-10 shrink-0 {folder
					? 'text-zen-highlight-foreground'
					: 'text-muted-foreground'}"
				aria-hidden="true"
			></span>
			<span
				class="line-clamp-2 w-full text-sm wrap-anywhere underline-offset-4 group-hover:underline {folder
					? 'font-medium'
					: ''}"
			>
				{entryName(entry.id)}
			</span>
		</button>
		{#if tileDetail(entry)}
			<span
				class="text-xs tabular-nums {entry.broken
					? 'font-medium text-amber-600 dark:text-amber-400'
					: 'text-muted-foreground'}"
			>
				{tileDetail(entry)}
			</span>
		{/if}
	</div>
{/snippet}

{#snippet selectionLabel(label: string, shortLabel: string, iconOnly: boolean)}
	{#if iconOnly}
		<span class="sr-only">{label}</span>
	{:else if shortLabel}
		<span class="@md:hidden">{shortLabel}</span>
		<span class="hidden @md:inline">{label}</span>
	{:else}
		<span>{label}</span>
	{/if}
{/snippet}

{#snippet selectionButton(
	label: string,
	icon: string,
	blocked: string,
	onclick: () => void,
	{
		destructive = false,
		iconOnly = false,
		shortLabel = ''
	}: { destructive?: boolean; iconOnly?: boolean; shortLabel?: string } = {}
)}
	{#if blocked || iconOnly}
		<Tooltip.Root delayDuration={500}>
			<Tooltip.Trigger
				type="button"
				aria-disabled={blocked ? 'true' : undefined}
				class={cn(
					buttonVariants({ variant: 'outline', size: iconOnly ? 'icon' : 'sm' }),
					iconOnly ? 'size-9' : 'h-9 gap-2',
					blocked && 'cursor-help opacity-50 aria-disabled:pointer-events-auto',
					destructive && 'text-destructive'
				)}
				onclick={blocked ? undefined : onclick}
			>
				<span class="{icon} size-4" aria-hidden="true"></span>
				{@render selectionLabel(label, shortLabel, iconOnly)}
				{#if blocked}
					<span class="sr-only">: {blocked}</span>
				{/if}
			</Tooltip.Trigger>
			<Tooltip.Content side="bottom" align="end" sideOffset={6} class="max-w-64 text-left">
				{#if iconOnly}
					<p class="font-medium">{label}</p>
				{/if}
				{#if blocked}
					<p>{blocked}</p>
				{/if}
			</Tooltip.Content>
		</Tooltip.Root>
	{:else}
		<Button
			variant="outline"
			size="sm"
			class="h-9 gap-2 {destructive ? 'text-destructive' : ''}"
			{onclick}
		>
			<span class="{icon} size-4" aria-hidden="true"></span>
			{@render selectionLabel(label, shortLabel, false)}
		</Button>
	{/if}
{/snippet}

{#snippet tableToolbar()}
	{#if selected.length > 0}
		{@render selectionButton(
			'Download',
			'icon-[lucide--download]',
			selectedDownloadBlock,
			() => void download(selected),
			{ iconOnly: true }
		)}
		{@render selectionButton(
			'Copy To…',
			'icon-[lucide--copy]',
			copyBlock,
			() => openTransfer('copy', selected),
			{ iconOnly: true }
		)}
		{@render selectionButton(
			'Move To…',
			'icon-[lucide--folder-input]',
			selectedBlock,
			() => openTransfer('move', selected),
			{ iconOnly: true }
		)}
		{@render selectionButton(
			`Delete Selected (${selected.length})`,
			'icon-[lucide--trash-2]',
			selectedBlock,
			() => openDelete(selected),
			{ destructive: true, shortLabel: `Delete (${selected.length})` }
		)}
	{/if}
	{@render viewToggle()}
{/snippet}

{#snippet viewToggle()}
	<div
		class="bg-background ml-auto flex h-9 items-center gap-0.5 rounded-md border p-0.5"
		role="group"
	>
		{#each views as option (option.value)}
			<button
				type="button"
				class="focus-visible:ring-ring flex size-8 cursor-pointer items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none {view.current ===
				option.value
					? 'bg-accent text-foreground'
					: 'text-muted-foreground hover:text-foreground'}"
				aria-pressed={view.current === option.value}
				aria-label={option.label}
				title={option.label}
				onclick={() => (view.current = option.value)}
			>
				<span class="{option.icon} size-4" aria-hidden="true"></span>
			</button>
		{/each}
	</div>
{/snippet}

{#snippet nameText(entry: FileNode)}
	{#if isFolder(entry)}
		<button
			type="button"
			class="focus-visible:ring-ring min-w-0 cursor-pointer truncate rounded-sm text-left font-medium underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
			title={entry.id}
			onclick={() => open(entry.id)}
		>
			{entryName(entry.id)}
		</button>
	{:else}
		<button
			type="button"
			class="focus-visible:ring-ring min-w-0 cursor-pointer truncate rounded-sm text-left underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
			title={entry.id}
			aria-haspopup="dialog"
			onclick={() => (detailsId = entry.id)}
		>
			{entryName(entry.id)}
		</button>
	{/if}
{/snippet}

{#snippet nameCell(entry: FileNode)}
	<div class="flex min-w-0 items-center gap-2">
		<span
			class="{entryIcon(entry)} size-4 shrink-0 {isFolder(entry)
				? 'text-zen-highlight-foreground'
				: 'text-muted-foreground'}"
			aria-hidden="true"
		></span>
		{@render nameText(entry)}
		{@render markers(entry)}
	</div>
{/snippet}

{#snippet sizeCell(entry: FileNode)}
	{#if canDownload(entry)}
		<span class="whitespace-nowrap tabular-nums">{formatBytesBinary(entry.size ?? 0)}</span>
	{:else}
		<span class="text-muted-foreground" aria-hidden="true">-</span>
		<span class="sr-only">No size</span>
	{/if}
{/snippet}

{#snippet dateCell(date: Date)}
	<span class="whitespace-nowrap" title={date.toLocaleString()}>{dateToAgo(date)}</span>
{/snippet}

{#snippet mutedCell(value: string)}
	<span class="text-muted-foreground whitespace-nowrap">{value}</span>
{/snippet}

{#snippet monoCell(value: string)}
	<span class="text-muted-foreground font-mono text-xs whitespace-nowrap">{value}</span>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, FileNode>)}
	{@const entry = row.original}
	<div class="mb-3 flex min-w-0 items-center gap-2">
		<span
			class="{entryIcon(entry)} size-4 shrink-0 {isFolder(entry)
				? 'text-zen-highlight-foreground'
				: 'text-muted-foreground'}"
			aria-hidden="true"
		></span>
		{@render nameText(entry)}
		{@render markers(entry)}
		<div class="-my-2 -mr-2 ml-auto shrink-0">{@render rowActions(entry)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		{#if canDownload(entry)}
			<div>
				<dt class="text-muted-foreground">Size</dt>
				<dd class="mt-1">{@render sizeCell(entry)}</dd>
			</div>
		{/if}
		<div>
			<dt class="text-muted-foreground">Modified</dt>
			<dd class="mt-1">{@render dateCell(entry.date)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Owner</dt>
			<dd class="mt-1">{ownerLabel(entry) || 'Not Reported'}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Permissions</dt>
			<dd class="mt-1 font-mono">{permissionString(entry) || 'Not Reported'}</dd>
		</div>
	</dl>
{/snippet}

{#snippet pathBar()}
	<div class="bg-card mb-4 flex h-11 min-w-0 items-center gap-1 rounded-lg border px-1.5">
		<Button
			variant="ghost"
			size="icon"
			class="size-8 shrink-0"
			disabled={path === '/'}
			onclick={() => open(parentPath(path))}
			aria-label="Go to the parent folder"
			title="Go to the parent folder"
		>
			<span class="icon-[lucide--arrow-up] size-4" aria-hidden="true"></span>
		</Button>
		{#if editingPath}
			<form class="flex min-w-0 flex-1 items-center gap-2" onsubmit={submitPath}>
				<Input
					bind:value={pathDraft}
					class="h-8 min-w-0 flex-1 font-mono text-sm"
					aria-label="Folder path"
					aria-invalid={pathError ? true : undefined}
					autocomplete="off"
					spellcheck={false}
					{@attach (input: HTMLInputElement) => {
						input.focus();
						input.select();
					}}
					onkeydown={(event) => {
						if (event.key === 'Escape') {
							event.preventDefault();
							editingPath = false;
						}
					}}
					onblur={() => (editingPath = false)}
				/>
				{#if pathError}
					<span class="text-destructive shrink-0 pr-1 text-xs" role="alert">({pathError})</span>
				{/if}
			</form>
		{:else}
			<ScrollArea
				bind:ref={crumbs}
				orientation="horizontal"
				scrollbarXClasses="hidden"
				class="min-w-0 flex-1"
			>
				<nav aria-label="Folder path">
					<ol class="flex h-8 items-center gap-0.5 text-sm whitespace-nowrap">
						<li class="flex items-center">
							{#if path === '/'}
								<span class="flex h-7 items-center gap-1.5 px-1.5 font-medium" aria-current="page">
									<span class="icon-[lucide--hard-drive] size-4" aria-hidden="true"></span>
									<span class="sr-only">Root</span>/
								</span>
							{:else}
								<button
									type="button"
									class="hover:bg-accent focus-visible:ring-ring flex h-7 cursor-pointer items-center gap-1.5 rounded-sm px-1.5 focus-visible:ring-2 focus-visible:outline-none"
									onclick={() => open('/')}
								>
									<span class="icon-[lucide--hard-drive] size-4" aria-hidden="true"></span>
									<span class="sr-only">Root</span>/
								</button>
							{/if}
						</li>
						{#each segments as segment (segment.path)}
							<li class="flex items-center gap-0.5">
								<span
									class="icon-[lucide--chevron-right] text-muted-foreground/60 size-3.5 shrink-0"
									aria-hidden="true"
								></span>
								{#if segment.path === path}
									<span class="px-1.5 font-medium" aria-current="page">{segment.name}</span>
								{:else}
									<button
										type="button"
										class="hover:bg-accent focus-visible:ring-ring h-7 cursor-pointer rounded-sm px-1.5 focus-visible:ring-2 focus-visible:outline-none"
										onclick={() => open(segment.path)}
									>
										{segment.name}
									</button>
								{/if}
							</li>
						{/each}
					</ol>
				</nav>
			</ScrollArea>
			<Button
				variant="ghost"
				size="icon"
				class="size-8 shrink-0"
				onclick={startEditing}
				aria-label="Type a path"
				title="Type a path"
			>
				<span class="icon-[lucide--pencil] size-4" aria-hidden="true"></span>
			</Button>
		{/if}
	</div>
{/snippet}

{#snippet uploadLabel()}
	<span class="icon-[lucide--upload] size-4" aria-hidden="true"></span>Upload
{/snippet}

{#snippet blockedHeaderButton(variant: 'default' | 'outline', content: Snippet)}
	<Tooltip.Root delayDuration={500}>
		<Tooltip.Trigger
			type="button"
			aria-disabled="true"
			class={cn(
				buttonVariants({ variant }),
				'h-9 cursor-help gap-2 opacity-50 aria-disabled:pointer-events-auto'
			)}
		>
			{@render content()}
			<span class="sr-only">: {newBlock}</span>
		</Tooltip.Trigger>
		<Tooltip.Content side="bottom" align="end" sideOffset={6} class="max-w-64 text-left">
			{newBlock}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet newLabel()}
	<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New
	<span class="icon-[lucide--chevron-down] size-4 opacity-70" aria-hidden="true"></span>
{/snippet}

{#snippet newMenu()}
	<DropdownMenu.Root>
		<DropdownMenu.Trigger>
			{#snippet child({ props })}
				<Button {...props} class="h-9 gap-2">{@render newLabel()}</Button>
			{/snippet}
		</DropdownMenu.Trigger>
		<DropdownMenu.Content
			align="end"
			class="w-48"
			onCloseAutoFocus={(event) => {
				if (nameDialog) event.preventDefault();
			}}
		>
			<MenuAction
				label="New Folder"
				icon="icon-[lucide--folder-plus]"
				onSelect={() => openNameDialog('folder')}
			/>
			<MenuAction
				label="New File"
				icon="icon-[lucide--file-plus]"
				onSelect={() => openNameDialog('file')}
			/>
		</DropdownMenu.Content>
	</DropdownMenu.Root>
{/snippet}

<svelte:head><title>Explorer · {hostname}</title></svelte:head>

<div
	class="@container relative min-h-full px-4 pt-3 pb-4 md:px-5 md:pb-5"
	role="region"
	aria-label="Explorer"
	ondragenter={dragEnter}
	ondragover={dragOver}
	ondragleave={dragLeave}
	ondrop={drop}
>
	{#if dragging}
		<div
			class="border-zen-highlight-foreground/60 bg-background/80 pointer-events-none absolute inset-2 z-40 flex flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed text-center backdrop-blur-sm"
		>
			<span class="icon-[lucide--upload] text-zen-highlight-foreground size-10" aria-hidden="true"
			></span>
			<p class="font-medium">Drop files to upload</p>
			<p class="text-muted-foreground max-w-sm px-4 font-mono text-xs break-all">{path}</p>
		</div>
	{/if}
	<header class="mb-4 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[bxs--folder-open] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Explorer</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex items-center gap-2">
			{#if newBlock}
				{@render blockedHeaderButton('outline', uploadLabel)}
				{@render blockedHeaderButton('default', newLabel)}
			{:else}
				<Button variant="outline" class="h-9 gap-2" onclick={() => openUpload()}>
					{@render uploadLabel()}
				</Button>
				{@render newMenu()}
			{/if}
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={loading}
				onclick={refresh}
				aria-label="Refresh folder"
			>
				<span
					class="icon-[lucide--refresh-cw] size-4"
					class:animate-spin={loading}
					aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>
	{@render pathBar()}
	{#if capped}
		<div
			class="bg-card mb-4 flex items-start gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span
				class="icon-[lucide--info] text-muted-foreground mt-0.5 size-4 shrink-0"
				aria-hidden="true"
			></span>
			<p class="min-w-0 flex-1 leading-relaxed">
				{#if !capped.query}
					This folder has {capped.total.toLocaleString()} items, so only the first
					{LIST_LIMIT.toLocaleString()} are shown, folders first and then by name. Search to find any
					item in it.
				{:else if capped.truncated}
					{capped.matched.toLocaleString()} of its {capped.total.toLocaleString()} items match, so only
					the first {LIST_LIMIT.toLocaleString()} are shown. Refine the search to narrow it down.
				{:else}
					Showing all {capped.matched.toLocaleString()}
					{capped.matched === 1 ? 'match' : 'matches'} among this folder’s {capped.total.toLocaleString()}
					items.
				{/if}
			</p>
		</div>
	{/if}
	{#if stale}
		<div
			class="bg-card mb-4 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span
				class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
				aria-hidden="true"
			></span>
			<p class="min-w-0 flex-1">
				This folder could not be refreshed. Showing the last successful listing.
			</p>
			<Button variant="outline" size="sm" disabled={loading} onclick={refresh}>Try Again</Button>
		</div>
	{/if}
	{#if missing}
		<div
			class="bg-card flex flex-col items-center gap-3 rounded-lg border px-6 py-12 text-center"
			role="status"
		>
			<span class="icon-[lucide--folder-x] text-muted-foreground size-8" aria-hidden="true"></span>
			<p class="font-medium">{missing}</p>
			<p class="text-muted-foreground font-mono text-xs break-all">{path}</p>
			<div class="flex flex-wrap justify-center gap-2">
				{#if path !== '/'}
					<Button variant="outline" size="sm" onclick={() => open(parentPath(path))}>
						Go to Parent Folder
					</Button>
				{/if}
				<Button variant="outline" size="sm" disabled={loading} onclick={refresh}>Try Again</Button>
			</div>
		</div>
	{:else}
		<DataTable
			data={current}
			{columns}
			getRowId={(entry) => entry.id}
			getRowLabel={(entry) => entryName(entry.id)}
			globalFilterFn={(row, _columnId, value) =>
				entryName(row.original.id)
					.toLocaleLowerCase()
					.includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'name', desc: false }]}
			pageResetKey={path}
			initialPageSize={50}
			pageSizeOptions={[50, 100, 250]}
			columnClasses={{
				name: 'min-w-56',
				size: 'pr-6 text-right',
				owner: 'hidden lg:table-cell',
				permissions: 'hidden xl:table-cell',
				actions: 'w-14 pr-3'
			}}
			loading={loading && shownPath !== path}
			searchPlaceholder="Search this folder…"
			emptyMessage="This folder is empty."
			view={view.current === 'grid' ? 'grid' : 'table'}
			{gridItem}
			toolbar={tableToolbar}
			toolbarRow={selected.length > 0}
			selectable
			bind:selection
			bind:searchText
			{mobileRow}
		/>
	{/if}
</div>

{#if details}
	<Details entry={details} onClose={() => (detailsId = '')} />
{/if}

{#if deleteTargets}
	<Delete
		{hostname}
		targets={deleteTargets}
		onClose={() => (deleteTargets = null)}
		onDone={refresh}
	/>
{/if}

{#if upload}
	{#key upload.session}
		<Upload
			{hostname}
			directory={path}
			files={upload.files}
			onClose={() => (upload = null)}
			onUploaded={refresh}
		/>
	{/key}
{/if}

{#if transfer}
	<Transfer
		{hostname}
		mode={transfer.mode}
		items={transfer.items}
		start={path}
		onClose={() => (transfer = null)}
		onDone={refresh}
		onOpen={open}
	/>
{/if}

{#if nameDialog && (nameDialog.mode !== 'rename' || renaming)}
	{#key nameDialog.session}
		<NameDialog
			{hostname}
			mode={nameDialog.mode}
			directory={path}
			entry={renaming}
			taken={names}
			onClose={() => (nameDialog = null)}
			onDone={refresh}
		/>
	{/key}
{/if}
