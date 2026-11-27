<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getNFSSharesResult, getNFSStatusResult, setNFSShareEnabled } from '$lib/api/nfs';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import MenuAction from '$lib/components/zen/Storage/Disks/MenuAction.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { NFSShare, NFSStatus } from '$lib/types/nfs';
	import {
		APIRequestError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { cachedStatus, datasetBlockReason } from '../shared';
	import ApplyDialog from './ApplyDialog.svelte';
	import Delete from './Delete.svelte';
	import Editor from './Editor.svelte';
	import StatusStrip from './StatusStrip.svelte';
	import {
		applyPending,
		clientsSummary,
		enableBlock,
		mappingSummary,
		pendingDetail,
		requestError,
		securityLabel,
		shareState,
		shareTitle,
		type SharesData
	} from './nfs';

	let { data }: { data: SharesData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.shares));

	const shares = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getNFSSharesResult({ hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('nfs-shares', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.shares) ? initial.shares : [],
			lazy: initial.fresh
		}
	);

	const status = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }): Promise<NFSStatus | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getNFSStatusResult({ hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			void updateCache('nfs-status', result, hostname);
			return result;
		},
		{
			initialValue: cachedStatus(initial.status),
			lazy: initial.statusFresh && Boolean(cachedStatus(initial.status))
		}
	);

	let failed = $derived(initialFailure || Boolean(shares.error));
	let rows = $derived(
		[...shares.current].sort((a, b) => shareTitle(a).localeCompare(shareTitle(b)) || a.id - b.id)
	);
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let applying = $derived(
		status.current?.applyStatus === 'applying' || status.current?.applyStatus === 'starting'
	);
	let restarts = $derived(Boolean(status.current?.enabled && status.current.runtimeAvailable));
	let selection = $state<RowSelectionState>({});
	let selectedRows = $derived(rows.filter((share) => selection[rowId(share)]));
	let deleteTargets = $state<NFSShare[] | null>(null);
	let applyOpen = $state(false);
	let toggling = $state<Record<number, boolean>>({});
	let editor = $state<{ session: number; id?: number } | null>(null);
	let editing = $derived(
		editor?.id === undefined ? undefined : rows.find((share) => share.id === editor?.id)
	);
	let editorSession = 0;

	function rowId(share: NFSShare): string {
		return String(share.id);
	}

	function openEditor(id?: number) {
		editor = { session: ++editorSession, id };
	}

	function refresh() {
		if (!shares.loading) void shares.refetch();
		if (!status.loading) void status.refetch();
	}

	async function setEnabled(share: NFSShare, enabled: boolean) {
		toggling[share.id] = true;
		const title = shareTitle(share);
		try {
			const response = await setNFSShareEnabled(share.id, enabled, {
				hostname: initial.hostname,
				signal: lifetime.signal
			});
			if (lifetime.signal.aborted) return;
			if (response.status !== 'success') {
				toast.error(
					requestError(response, `The share could not be ${enabled ? 'enabled' : 'disabled'}.`),
					{ position: 'bottom-center' }
				);
			} else if (applyPending(response)) {
				toast.warning(pendingDetail(response), { position: 'bottom-center' });
			} else {
				toast.success(`Share of ${title} ${enabled ? 'enabled' : 'disabled'}`, {
					position: 'bottom-center'
				});
			}
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			toast.error(`The share could not be ${enabled ? 'enabled' : 'disabled'}.`, {
				position: 'bottom-center'
			});
		} finally {
			if (!lifetime.signal.aborted) {
				delete toggling[share.id];
				refresh();
			}
		}
	}

	const interval = useInterval(() => (applying ? 2_000 : storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (!storage.visible) return;
			if (!applying) refresh();
			else if (!status.loading) void status.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle) refresh();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, NFSShare>();
	const columns = helper.columns([
		helper.accessor((share) => shareState(share, status.current).label, {
			id: 'status',
			header: 'Status',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(statusCell, row.original)
		}),
		helper.accessor((share) => shareTitle(share), {
			id: 'dataset',
			header: 'Dataset',
			sortFn: 'text',
			cell: ({ row }) => renderSnippet(datasetCell, row.original)
		}),
		helper.accessor((share) => clientsSummary(share), {
			id: 'clients',
			header: 'Clients',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(clientsCell, row.original)
		}),
		helper.accessor((share) => (share.readOnly ? 'Read Only' : 'Read & Write'), {
			id: 'access',
			header: 'Access',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(accessCell, row.original)
		}),
		helper.accessor((share) => share.security.join(' '), {
			id: 'security',
			header: 'Security',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(securityCell, row.original)
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

{#snippet rowActions(share: NFSShare)}
	{@const title = shareTitle(share)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for the share of {title}"
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
					if (editor || deleteTargets) event.preventDefault();
				}}
			>
				{#if canModify}
					<MenuAction
						label="Edit"
						icon="icon-[lucide--pencil]"
						onSelect={() => openEditor(share.id)}
					/>
					{#if share.enabled}
						<MenuAction
							label="Disable"
							icon="icon-[lucide--circle-pause]"
							blocked={toggling[share.id] ? 'The share is being updated' : ''}
							onSelect={() => void setEnabled(share, false)}
						/>
					{:else}
						<MenuAction
							label="Enable"
							icon="icon-[lucide--circle-play]"
							blocked={toggling[share.id] ? 'The share is being updated' : enableBlock(share)}
							onSelect={() => void setEnabled(share, true)}
						/>
					{/if}
					<DropdownMenu.Separator />
					<MenuAction
						label="Delete"
						icon="icon-[lucide--trash-2]"
						destructive
						onSelect={() => (deleteTargets = [share])}
					/>
				{:else}
					<MenuAction
						label="View Settings"
						icon="icon-[lucide--eye]"
						onSelect={() => openEditor(share.id)}
					/>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet datasetCell(share: NFSShare)}
	{@const title = shareTitle(share)}
	{@const split = title.lastIndexOf('/') + 1}
	<div class="grid grid-cols-[minmax(0,1fr)] justify-items-start">
		<button
			type="button"
			class="focus-visible:ring-ring text-zen-highlight-foreground flex max-w-full min-w-0 cursor-pointer rounded-sm text-left font-medium underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
			aria-label="{canModify ? 'Edit' : 'View'} the share of {title}"
			aria-haspopup="dialog"
			onclick={() => openEditor(share.id)}
		>
			{#if split > 0}
				<span class="min-w-0 truncate">{title.slice(0, split)}</span>
				<span class="max-w-[70%] shrink-0 truncate">{title.slice(split)}</span>
			{:else}
				<span class="min-w-0 truncate">{title}</span>
			{/if}
		</button>
		{#if share.description}
			<span class="text-muted-foreground max-w-full truncate text-xs">{share.description}</span>
		{:else if share.mountpoint}
			<span class="text-muted-foreground max-w-full truncate font-mono text-xs"
				>{share.mountpoint}</span
			>
		{/if}
	</div>
{/snippet}

{#snippet clientsCell(share: NFSShare)}
	<div class="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2">
		<span
			class="{share.everyone
				? 'icon-[lucide--globe]'
				: 'icon-[lucide--network]'} text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		{#if !share.everyone && share.clients.length > 2}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring min-w-0 cursor-default truncate rounded-sm text-left font-mono text-xs focus-visible:ring-2 focus-visible:outline-none"
					aria-label="Clients: {share.clients.join(', ')}"
				>
					{clientsSummary(share)}
				</Tooltip.Trigger>
				<Tooltip.Content side="top" align="start" sideOffset={6} class="max-w-80 text-left">
					<ul class="space-y-0.5 font-mono">
						{#each share.clients as client (client)}<li>{client}</li>{/each}
					</ul>
				</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			<span class="truncate {share.everyone ? '' : 'font-mono text-xs'}"
				>{clientsSummary(share)}</span
			>
		{/if}
	</div>
{/snippet}

{#snippet accessCell(share: NFSShare)}
	<div class="grid grid-cols-[minmax(0,1fr)]">
		<span class="truncate">{share.readOnly ? 'Read Only' : 'Read & Write'}</span>
		<span class="text-muted-foreground truncate text-xs">{mappingSummary(share)}</span>
	</div>
{/snippet}

{#snippet securityCell(share: NFSShare)}
	<span class="flex flex-wrap gap-1">
		{#each share.security as flavor (flavor)}
			<span class="bg-muted text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs"
				>{securityLabel(flavor)}</span
			>
		{/each}
	</span>
{/snippet}

{#snippet statusCell(share: NFSShare)}
	{@const state = shareState(share, status.current)}
	<span class="flex items-center gap-2 whitespace-nowrap">
		{#if state.tone === 'busy'}
			<span
				class="icon-[lucide--loader-circle] text-muted-foreground size-3.5 shrink-0 animate-spin"
				aria-hidden="true"
			></span>
		{:else}
			<span
				class="size-2 shrink-0 rounded-full {state.tone === 'good'
					? 'bg-emerald-500'
					: state.tone === 'warning'
						? 'bg-amber-500'
						: 'bg-muted-foreground/50'}"
				aria-hidden="true"
			></span>
		{/if}
		{#if state.detail}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring cursor-help rounded-sm text-left underline decoration-dotted underline-offset-4 focus-visible:ring-2 focus-visible:outline-none"
				>
					{state.label}<span class="sr-only">: {state.detail}</span>
				</Tooltip.Trigger>
				<Tooltip.Content
					side="top"
					sideOffset={6}
					class="max-w-72 text-left break-words whitespace-pre-line"
				>
					{state.detail}
				</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			{state.label}
		{/if}
		{#if share.datasetBlock}
			{@const reason = datasetBlockReason(share.datasetBlock)}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring inline-flex cursor-help items-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
					aria-label="Dataset can’t be exported anymore: {reason}"
				>
					<span
						class="icon-[lucide--triangle-alert] size-3.5 text-amber-600 dark:text-amber-400"
						aria-hidden="true"
					></span>
				</Tooltip.Trigger>
				<Tooltip.Content side="top" sideOffset={6} class="max-w-64 text-left text-pretty">
					New shares can’t export this dataset anymore. {reason}. {share.enabled
						? 'This share keeps working, but once it’s disabled it can’t be enabled again.'
						: 'This share can’t be enabled again.'}
				</Tooltip.Content>
			</Tooltip.Root>
		{/if}
	</span>
{/snippet}

{#snippet bulkActions()}
	<Button
		variant="outline"
		size="sm"
		class="text-destructive h-9 gap-2"
		onclick={() => (deleteTargets = selectedRows)}
	>
		<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>
		Delete Selected ({selectedRows.length})
	</Button>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, NFSShare>)}
	{@const share = row.original}
	<div class="mb-3 flex min-w-0 items-start gap-2">
		<div class="min-w-0 flex-1">{@render datasetCell(share)}</div>
		<div class="-my-2 -mr-2 shrink-0">{@render rowActions(share)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div class="min-w-0">
			<dt class="text-muted-foreground">Status</dt>
			<dd class="mt-1">{@render statusCell(share)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Clients</dt>
			<dd class="mt-1">{@render clientsCell(share)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Access</dt>
			<dd class="mt-1">{@render accessCell(share)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Security</dt>
			<dd class="mt-1">{@render securityCell(share)}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>NFS Shares · {initial.hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[mdi--folder-network] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">NFS</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Shares</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex items-center gap-2">
			{#if canModify}
				<Button class="h-9 gap-2" onclick={() => openEditor()}>
					<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New Share
				</Button>
			{/if}
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={shares.loading}
				onclick={refresh}
				aria-label="Refresh shares"
			>
				<span
					class="icon-[lucide--refresh-cw] size-4"
					class:animate-spin={shares.loading}
					aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>
	<StatusStrip
		status={status.current}
		failed={Boolean(status.error)}
		loading={status.loading}
		subject="these shares aren’t exported"
		onApply={canModify ? () => (applyOpen = true) : undefined}
	/>
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
				{rows.length > 0
					? 'Shares could not be refreshed. Showing the last successful list.'
					: 'Shares could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={shares.loading} onclick={refresh}>
				Try Again
			</Button>
		</div>
	{/if}
	{#if !failed || rows.length > 0}
		<DataTable
			data={rows}
			{columns}
			getRowId={rowId}
			getRowLabel={(share) => `the share of ${shareTitle(share)}`}
			globalFilterFn={(row, _columnId, value) =>
				[
					shareTitle(row.original),
					row.original.mountpoint,
					row.original.description,
					...row.original.clients
				]
					.join(' ')
					.toLocaleLowerCase()
					.includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'dataset', desc: false }]}
			bind:selection
			selectable={canModify}
			initialPageSize={25}
			pageSizeOptions={[25, 50, 100]}
			columnClasses={{
				dataset: 'min-w-48 max-w-80',
				clients: 'min-w-36',
				access: 'min-w-32',
				actions: 'w-14 pr-3'
			}}
			loading={shares.loading}
			searchPlaceholder="Search datasets, descriptions or clients…"
			fullWidthSearch
			emptyMessage="No NFS shares on this node."
			toolbar={canModify && selectedRows.length > 0 ? bulkActions : undefined}
			{mobileRow}
		/>
	{/if}
</div>

{#if editor && (editor.id === undefined || editing)}
	{#key editor.session}
		<Editor
			hostname={initial.hostname}
			share={editing}
			readOnly={!canModify}
			onClose={() => (editor = null)}
			onSaved={refresh}
		/>
	{/key}
{/if}

{#if deleteTargets}
	<Delete
		hostname={initial.hostname}
		targets={deleteTargets}
		{restarts}
		onClose={() => (deleteTargets = null)}
		onDone={(deleted) => {
			selection = Object.fromEntries(
				Object.entries(selection).filter(([id]) => !deleted.includes(Number(id)))
			);
			refresh();
		}}
	/>
{/if}

{#if applyOpen}
	<ApplyDialog hostname={initial.hostname} onClose={() => (applyOpen = false)} onDone={refresh} />
{/if}
