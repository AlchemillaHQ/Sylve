<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { bulkDelete, bulkDeletePeriodicSnapshots } from '$lib/api/zfs/datasets';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SnapshotSummary } from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { snapshotDeleteErrorMessage, snapshotScheduleLabel } from '$lib/utils/zfs/snapshot';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		chunked,
		scheduleCleanup,
		scheduleDeleteErrorMessage,
		type ScheduleRow
	} from './snapshots';

	let {
		hostname,
		targets,
		rows,
		snapshots,
		onClose,
		onDone
	}: {
		hostname: string;
		targets: ScheduleRow[];
		rows: ScheduleRow[];
		snapshots: SnapshotSummary[];
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const batchSize = 200;
	const listLimit = 200;
	const id = $props.id();
	const lifetime = new AbortController();
	const chosen = untrack(() => [...targets]);
	const single = chosen.length === 1 ? chosen[0] : undefined;
	const cleanup = untrack(() => scheduleCleanup(chosen, rows, snapshots));
	const freed = cleanup.deletable.reduce((total, snapshot) => total + snapshot.used, 0);

	let includeSnapshots = $state(false);
	let typed = $state('');
	let deleting = $state(false);
	let schedulesDeleted = $state(false);
	let snapshotsDeleted = $state(0);
	let deleteError = $state('');
	let removing = $derived(includeSnapshots ? cleanup.deletable.length : 0);
	let confirmation = $derived(removing > 0 ? 'delete' : '');
	let canConfirm = $derived(!deleting && (!confirmation || typed.trim() === confirmation));

	onDestroy(() => lifetime.abort());

	function plural(count: number, one: string, many: string): string {
		return `${count.toLocaleString()} ${count === 1 ? one : many}`;
	}

	function scheduleName(row: ScheduleRow): string {
		return row.dataset ? `${row.dataset}@${row.job.prefix}-…` : `${row.job.prefix}-…`;
	}

	async function deleteSchedules() {
		const response = await bulkDeletePeriodicSnapshots(
			chosen.map((row) => row.job.id),
			{ hostname, signal: lifetime.signal }
		);
		if (response.status !== 'success') {
			handleAPIError(response);
			throw new APIRequestError(response);
		}
		schedulesDeleted = true;
	}

	async function deleteSnapshots() {
		const pending = cleanup.deletable.slice(snapshotsDeleted);
		for (const batch of chunked(pending, batchSize)) {
			const response = await bulkDelete(
				batch.map((snapshot) => ({ name: snapshot.name, guid: snapshot.guid })),
				{ hostname, signal: lifetime.signal }
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			snapshotsDeleted += batch.length;
		}
	}

	async function confirm() {
		if (!canConfirm) return;
		deleting = true;
		deleteError = '';
		try {
			if (!schedulesDeleted) await deleteSchedules();
			if (removing > 0) await deleteSnapshots();
			const schedules = single ? 'Snapshot schedule' : `${chosen.length} snapshot schedules`;
			toast.success(
				removing > 0
					? `${schedules} and ${plural(removing, 'snapshot', 'snapshots')} deleted`
					: `${schedules} deleted`,
				{ position: 'bottom-center' }
			);
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			const message = error instanceof Error ? error.message : 'The request failed.';
			deleteError = schedulesDeleted
				? `${single ? 'The schedule was' : 'The schedules were'} deleted and ${plural(snapshotsDeleted, 'snapshot', 'snapshots')} of ${removing.toLocaleString()} removed. ${snapshotDeleteErrorMessage(message)}`
				: scheduleDeleteErrorMessage(message);
			onDone();
		} finally {
			if (!lifetime.signal.aborted) deleting = false;
		}
	}
</script>

{#snippet snapshotList(entries: SnapshotSummary[], label: string)}
	<ScrollArea
		class="rounded-md border"
		fade
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'max-h-40 overscroll-contain',
			role: 'region',
			'aria-label': label,
			tabindex: 0
		}}
	>
		<ul class="divide-y text-sm">
			{#each entries.slice(0, listLimit) as entry (entry.name)}
				<li class="px-3 py-2 font-mono text-xs break-words">{entry.name}</li>
			{/each}
			{#if entries.length > listLimit}
				<li class="text-muted-foreground px-3 py-2 text-xs">
					and {(entries.length - listLimit).toLocaleString()} more
				</li>
			{/if}
		</ul>
	</ScrollArea>
{/snippet}

<AlertDialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !deleting) onClose();
	}}
>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={deleting ? 'ignore' : 'close'}
		aria-busy={deleting}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					{single ? 'Delete Snapshot Schedule?' : `Delete ${chosen.length} Snapshot Schedules?`}
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={deleting}>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={deleting}
					>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				{#if single}
					Sylve stops taking scheduled snapshots of
					<span class="text-foreground text-sm font-semibold break-words"
						>{single.dataset || 'this file system'}</span
					>
					on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					({snapshotScheduleLabel(single.job.interval, single.job.cronExpr)}).
				{:else}
					Sylve stops taking snapshots on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					with these schedules.
				{/if}
				{#if !includeSnapshots}
					The snapshots {single ? 'it' : 'they'} already took are kept.
				{/if}
			</AlertDialog.Description>
			{#if !single}
				<ScrollArea
					class="rounded-md border"
					fade
					scrollbarYClasses="hidden"
					viewportProps={{
						class: 'max-h-40 overscroll-contain',
						role: 'region',
						'aria-label': 'Schedules to delete',
						tabindex: 0
					}}
				>
					<ul class="divide-y text-sm">
						{#each chosen as row (row.job.id)}
							<li class="flex items-baseline justify-between gap-3 px-3 py-2 text-xs">
								<span class="min-w-0 font-mono break-words">{scheduleName(row)}</span>
								<span class="text-muted-foreground shrink-0 whitespace-nowrap">
									{snapshotScheduleLabel(row.job.interval, row.job.cronExpr)}
								</span>
							</li>
						{/each}
					</ul>
				</ScrollArea>
			{/if}
			{#if cleanup.deletable.length + cleanup.shared.length + cleanup.blocked.length > 0}
				<div class="space-y-2 rounded-md border p-3 text-sm">
					{#if cleanup.deletable.length > 0}
						<div class="flex items-start gap-2">
							<Checkbox
								id="{id}-snapshots"
								class="mt-0.5"
								bind:checked={includeSnapshots}
								disabled={deleting || schedulesDeleted}
							/>
							<Label for="{id}-snapshots" class="block leading-snug font-normal">
								Also delete the {plural(cleanup.deletable.length, 'snapshot', 'snapshots')}
								{single ? 'it' : 'they'} took
							</Label>
						</div>
						{#if includeSnapshots}
							{@render snapshotList(cleanup.deletable, 'Snapshots to delete')}
							<p class="text-muted-foreground text-xs leading-5">
								{freed > 0
									? `Frees at least ${formatBytesBinary(freed)}.`
									: 'Frees little or no space, because their data is still shared with the file system or other snapshots.'}
							</p>
						{/if}
					{/if}
					{#if cleanup.shared.length > 0}
						<p class="text-muted-foreground text-xs leading-5">
							{plural(cleanup.shared.length, 'snapshot', 'snapshots')}
							{cleanup.shared.length === 1 ? 'matches' : 'match'} another schedule too, so
							{cleanup.shared.length === 1 ? 'it stays' : 'they stay'}.
						</p>
					{/if}
					{#if cleanup.blocked.length > 0}
						<p class="text-muted-foreground text-xs leading-5">
							{plural(cleanup.blocked.length, 'snapshot', 'snapshots')}
							{cleanup.blocked.length === 1 ? 'has' : 'have'} clones or holds, or
							{cleanup.blocked.length === 1 ? 'is' : 'are'} used by Sylve, so
							{cleanup.blocked.length === 1 ? 'it stays' : 'they stay'}.
						</p>
					{/if}
				</div>
			{/if}
			{#if confirmation}
				<div class="space-y-2">
					<Label for="{id}-confirm" class="block leading-relaxed">
						Type <span class="text-foreground text-sm font-semibold">{confirmation}</span> to confirm
					</Label>
					<Input
						id="{id}-confirm"
						bind:value={typed}
						placeholder={confirmation}
						autocomplete="off"
						spellcheck={false}
						disabled={deleting}
						onkeydown={(event) => {
							if (event.key === 'Enter') void confirm();
						}}
					/>
				</div>
			{/if}
			{#if deleteError}
				<p class="text-destructive text-sm break-words" role="alert">{deleteError}</p>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={!canConfirm} onclick={confirm}>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					{schedulesDeleted && removing > 0
						? `Deleting ${snapshotsDeleted.toLocaleString()} of ${removing.toLocaleString()}…`
						: 'Deleting…'}
				{:else if schedulesDeleted}
					Delete Remaining Snapshots
				{:else}
					{single ? 'Delete Schedule' : `Delete ${chosen.length} Schedules`}
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
