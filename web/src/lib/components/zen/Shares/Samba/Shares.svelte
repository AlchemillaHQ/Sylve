<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import {
		cancelSharePermissionJob,
		getSambaSharesResult,
		getSambaStatus,
		setSambaShareEnabled
	} from '$lib/api/samba/share';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import MenuAction from '$lib/components/zen/Storage/Disks/MenuAction.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { SambaShare, SambaStatus } from '$lib/types/samba/shares';
	import {
		APIRequestError,
		getAPIErrorText,
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
	import Connections from './Connections.svelte';
	import Delete from './Delete.svelte';
	import Editor from './Editor.svelte';
	import StatusStrip from './StatusStrip.svelte';
	import {
		accessMode,
		accessSummary,
		connectionsByShare,
		datasetBlockReason,
		cachedStatus,
		enableBlock,
		jobSummary,
		shareSaveError,
		shareState,
		type SharesData
	} from './samba';

	let { data }: { data: SharesData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.shares));
	let now = $state(Date.now());

	const shares = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getSambaSharesResult({ hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			now = Date.now();
			void updateCache('samba-shares', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.shares) ? initial.shares : [],
			lazy: initial.fresh
		}
	);

	const status = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }): Promise<SambaStatus | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getSambaStatus({ hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			void updateCache('samba-status', result, hostname);
			return result;
		},
		{
			initialValue: cachedStatus(initial.status),
			lazy: initial.statusFresh && Boolean(cachedStatus(initial.status))
		}
	);

	let failed = $derived(initialFailure || Boolean(shares.error));
	let rows = $derived([...shares.current].sort((a, b) => a.name.localeCompare(b.name)));
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let connections = $derived(connectionsByShare(status.current));
	let serving = $derived(Boolean(status.current?.running && !status.current.error));
	let jobRunning = $derived(rows.some((share) => share.permissionsJob?.state === 'running'));
	let selection = $state<RowSelectionState>({});
	let selectedRows = $derived(rows.filter((share) => selection[rowId(share)]));
	let deleteTargets = $state<SambaShare[] | null>(null);
	let disableTarget = $state<SambaShare | null>(null);
	let connectionsOpen = $state(false);
	let toggling = $state<Record<number, boolean>>({});
	let editor = $state<{ session: number; id?: number } | null>(null);
	let editing = $derived(
		editor?.id === undefined ? undefined : rows.find((share) => share.id === editor?.id)
	);
	let editorSession = 0;

	function rowId(share: SambaShare): string {
		return String(share.id);
	}

	function openEditor(id?: number) {
		editor = { session: ++editorSession, id };
	}

	function connectionCount(share: SambaShare): number | undefined {
		return serving ? (connections.get(share.name.toLowerCase()) ?? 0) : undefined;
	}

	function refresh() {
		now = Date.now();
		if (!shares.loading) void shares.refetch();
		if (!status.loading) void status.refetch();
	}

	function requestToggle(share: SambaShare) {
		if (share.enabled && (connectionCount(share) ?? 0) > 0) disableTarget = share;
		else void setEnabled(share, !share.enabled);
	}

	function confirmDisable() {
		const share = disableTarget;
		disableTarget = null;
		if (share) void setEnabled(share, false);
	}

	async function setEnabled(share: SambaShare, enabled: boolean) {
		toggling[share.id] = true;
		try {
			const response = await setSambaShareEnabled(share.id, enabled, {
				hostname: initial.hostname,
				signal: lifetime.signal
			});
			if (lifetime.signal.aborted) return;
			if (response.status === 'success') {
				toast.success(`Share ${share.name} ${enabled ? 'enabled' : 'disabled'}`, {
					position: 'bottom-center'
				});
			} else if (response.message === 'samba_apply_failed') {
				toast.warning(shareSaveError(response), { position: 'bottom-center' });
			} else {
				toast.error(shareSaveError(response), { position: 'bottom-center' });
			}
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			toast.error(`Share ${share.name} could not be ${enabled ? 'enabled' : 'disabled'}`, {
				position: 'bottom-center'
			});
		} finally {
			if (!lifetime.signal.aborted) {
				delete toggling[share.id];
				refresh();
			}
		}
	}

	async function stopJob(share: SambaShare) {
		try {
			const response = await cancelSharePermissionJob(share.id, {
				hostname: initial.hostname,
				signal: lifetime.signal
			});
			if (lifetime.signal.aborted) return;
			if (response.status === 'success') {
				toast.success(`Stopped applying access on ${share.name}`, { position: 'bottom-center' });
			} else if (response.message !== 'permissions_job_not_running') {
				toast.error(getAPIErrorText(response, 'The job could not be stopped.'), {
					position: 'bottom-center'
				});
			}
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			toast.error('The job could not be stopped.', { position: 'bottom-center' });
		} finally {
			if (!lifetime.signal.aborted) refresh();
		}
	}

	const interval = useInterval(
		() => (jobRunning ? 3_000 : connectionsOpen ? 5_000 : storage.idle ? 60_000 : 30_000),
		{
			callback: () => {
				if (storage.visible) refresh();
			}
		}
	);
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

	const helper = createColumnHelper<DataTableFeatures, SambaShare>();
	const columns = helper.columns([
		helper.accessor('name', {
			id: 'name',
			header: 'Name',
			sortFn: 'text',
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		}),
		helper.accessor((share) => share.datasetName || share.dataset, {
			id: 'dataset',
			header: 'Dataset',
			cell: ({ row }) => renderSnippet(datasetCell, row.original)
		}),
		helper.accessor((share) => accessSummary(share), {
			id: 'access',
			header: 'Access',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(accessCell, row.original)
		}),
		helper.accessor((share) => shareState(share).label, {
			id: 'status',
			header: 'Status',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(statusCell, row.original)
		}),
		helper.accessor((share) => connectionCount(share) ?? -1, {
			id: 'connections',
			header: 'Connections',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(connectionsCell, row.original)
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

{#snippet rowActions(share: SambaShare)}
	{@const job = share.permissionsJob}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for {share.name}"
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
					if (editor || deleteTargets || disableTarget) event.preventDefault();
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
							onSelect={() => requestToggle(share)}
						/>
					{:else}
						<MenuAction
							label="Enable"
							icon="icon-[lucide--circle-play]"
							blocked={toggling[share.id] ? 'The share is being updated' : enableBlock(share)}
							onSelect={() => requestToggle(share)}
						/>
					{/if}
					{#if job?.state === 'running'}
						<MenuAction
							label="Stop Applying Access"
							icon="icon-[lucide--octagon-x]"
							onSelect={() => void stopJob(share)}
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

{#snippet nameCell(share: SambaShare)}
	<div class="grid grid-cols-[minmax(0,1fr)] justify-items-start">
		<button
			type="button"
			class="focus-visible:ring-ring text-zen-highlight-foreground max-w-full cursor-pointer truncate rounded-sm text-left font-medium underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
			title={share.name}
			aria-label="{canModify ? 'Edit' : 'View'} {share.name}"
			aria-haspopup="dialog"
			onclick={() => openEditor(share.id)}
		>
			{share.name}
		</button>
	</div>
{/snippet}

{#snippet datasetCell(share: SambaShare)}
	{#if share.datasetName}
		{@const split = share.datasetName.lastIndexOf('/') + 1}
		<div class="grid grid-cols-[minmax(0,1fr)] justify-items-start">
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring flex max-w-full min-w-0 cursor-default rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
					aria-label={share.datasetName}
				>
					{#if split > 0}
						<span class="min-w-0 truncate">{share.datasetName.slice(0, split)}</span>
						<span class="max-w-[70%] shrink-0 truncate">{share.datasetName.slice(split)}</span>
					{:else}
						<span class="min-w-0 truncate">{share.datasetName}</span>
					{/if}
				</Tooltip.Trigger>
				<Tooltip.Content side="top" align="start" sideOffset={6} class="max-w-96 text-left">
					<p class="break-all">{share.datasetName}</p>
					{#if share.mountpoint.startsWith('/')}
						<p class="mt-0.5 break-all opacity-70">Mounted at {share.mountpoint}</p>
					{/if}
				</Tooltip.Content>
			</Tooltip.Root>
		</div>
	{:else if share.state}
		<span class="text-xs text-amber-600 dark:text-amber-400">Dataset Missing</span>
	{:else}
		<span class="text-muted-foreground text-xs">Unknown</span>
	{/if}
{/snippet}

{#snippet accessCell(share: SambaShare)}
	{@const guest = accessMode(share) !== 'signed-in'}
	<span class="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2">
		<span
			class="{guest
				? 'icon-[lucide--globe]'
				: 'icon-[lucide--users]'} text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		<span class="truncate">{accessSummary(share)}</span>
	</span>
{/snippet}

{#snippet statusCell(share: SambaShare)}
	{@const state = shareState(share)}
	{@const job = jobSummary(share.permissionsJob, now)}
	<div class="min-w-0 space-y-1">
		<span class="flex items-center gap-2 whitespace-nowrap">
			<span
				class="size-2 shrink-0 rounded-full {state.tone === 'good'
					? 'bg-emerald-500'
					: state.tone === 'warning'
						? 'bg-amber-500'
						: 'bg-muted-foreground/50'}"
				aria-hidden="true"
			></span>
			{state.label}
			{#if share.datasetBlock}
				{@const reason = datasetBlockReason(share.datasetBlock)}
				<Tooltip.Root delayDuration={500}>
					<Tooltip.Trigger
						type="button"
						class="focus-visible:ring-ring inline-flex cursor-help items-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
						aria-label="Dataset can’t be shared: {reason}"
					>
						<span
							class="icon-[lucide--triangle-alert] size-3.5 text-amber-600 dark:text-amber-400"
							aria-hidden="true"
						></span>
					</Tooltip.Trigger>
					<Tooltip.Content side="top" sideOffset={6} class="max-w-64 text-left text-pretty">
						This dataset can’t be shared anymore. {reason}. The share keeps working, but once it’s
						disabled it can’t be enabled again.
					</Tooltip.Content>
				</Tooltip.Root>
			{/if}
		</span>
		{#if job}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					class="focus-visible:ring-ring flex cursor-help items-center gap-1.5 rounded-sm text-xs whitespace-nowrap focus-visible:ring-2 focus-visible:outline-none {job.tone ===
					'warning'
						? 'text-amber-600 dark:text-amber-400'
						: 'text-muted-foreground'}"
				>
					<span
						class="{job.tone === 'busy'
							? 'icon-[lucide--loader-circle] animate-spin'
							: job.tone === 'warning'
								? 'icon-[lucide--triangle-alert]'
								: job.tone === 'good'
									? 'icon-[lucide--check]'
									: 'icon-[lucide--circle-minus]'} size-3.5 shrink-0"
						aria-hidden="true"
					></span>
					{job.label}
					{#if job.detail}<span class="sr-only">: {job.detail}</span>{/if}
				</Tooltip.Trigger>
				{#if job.detail}
					<Tooltip.Content side="top" sideOffset={6} class="max-w-72 text-left text-pretty">
						{job.detail}
					</Tooltip.Content>
				{/if}
			</Tooltip.Root>
		{/if}
	</div>
{/snippet}

{#snippet connectionsCell(share: SambaShare)}
	{@const count = connectionCount(share)}
	{#if count === undefined}
		<span class="text-muted-foreground">—</span>
	{:else}
		<span class="tabular-nums {count === 0 ? 'text-muted-foreground' : ''}">{count}</span>
	{/if}
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

{#snippet mobileRow(row: Row<DataTableFeatures, SambaShare>)}
	{@const share = row.original}
	<div class="mb-3 flex min-w-0 items-center gap-2">
		{@render nameCell(share)}
		<div class="-my-2 -mr-2 ml-auto shrink-0">{@render rowActions(share)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div class="min-w-0">
			<dt class="text-muted-foreground">Dataset</dt>
			<dd class="mt-1">{@render datasetCell(share)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Status</dt>
			<dd class="mt-1">{@render statusCell(share)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Access</dt>
			<dd class="mt-1">{@render accessCell(share)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Connections</dt>
			<dd class="mt-1">{@render connectionsCell(share)}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>Samba Shares · {initial.hostname}</title></svelte:head>

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
		subject="these shares aren’t available"
		onConnections={() => (connectionsOpen = true)}
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
			getRowLabel={(share) => share.name}
			globalFilterFn={(row, _columnId, value) =>
				`${row.original.name} ${row.original.datasetName} ${row.original.mountpoint}`
					.toLocaleLowerCase()
					.includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'name', desc: false }]}
			bind:selection
			selectable={canModify}
			initialPageSize={25}
			pageSizeOptions={[25, 50, 100]}
			columnClasses={{
				name: 'min-w-40',
				dataset: 'min-w-48 max-w-80',
				connections: 'pr-6 text-right',
				actions: 'w-14 pr-3'
			}}
			loading={shares.loading}
			searchPlaceholder="Search shares, datasets or paths…"
			fullWidthSearch
			emptyMessage="No Samba shares on this node."
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
			shares={rows}
			readOnly={!canModify}
			onClose={() => (editor = null)}
			onSaved={() => shares.refetch()}
		/>
	{/key}
{/if}

{#if deleteTargets}
	<Delete
		hostname={initial.hostname}
		targets={deleteTargets}
		{connectionCount}
		onClose={() => (deleteTargets = null)}
		onDone={(deleted) => {
			selection = Object.fromEntries(
				Object.entries(selection).filter(([id]) => !deleted.includes(Number(id)))
			);
			refresh();
		}}
	/>
{/if}

{#if connectionsOpen && status.current}
	<Connections status={status.current} onClose={() => (connectionsOpen = false)} />
{/if}

{#if disableTarget}
	{@const target = disableTarget}
	{@const count = connectionCount(target) ?? 0}
	<AlertDialog.Root
		open
		onOpenChange={(open) => {
			if (!open) disableTarget = null;
		}}
	>
		<AlertDialog.Content>
			<AlertDialog.Header class="text-left">
				<AlertDialog.Title>Disable {target.name}?</AlertDialog.Title>
				<AlertDialog.Description class="text-pretty">
					{count === 1 ? '1 client is' : `${count} clients are`} connected to this share and will lose
					access to it. The share’s settings and files are kept, and you can enable it again later.
				</AlertDialog.Description>
			</AlertDialog.Header>
			<AlertDialog.Footer>
				<AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
				<AlertDialog.Action onclick={confirmDisable}>Disable Share</AlertDialog.Action>
			</AlertDialog.Footer>
		</AlertDialog.Content>
	</AlertDialog.Root>
{/if}
