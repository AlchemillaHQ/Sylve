<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { page } from '$app/state';
	import {
		createPoolScrubSchedule,
		deletePoolScrubSchedule,
		getPoolStatusResult,
		listPoolScrubRuns,
		listPoolScrubSchedules,
		scrubPool,
		updatePoolScrubSchedule
	} from '$lib/api/zfs/pool';
	import ModalTable from '$lib/components/custom/ModalTable.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import {
		isPoolScrubActive,
		type PoolScrubRun,
		type PoolScrubSchedule,
		type Zpool,
		type ZpoolStatusPool
	} from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { getAPIErrorText, handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
	import { isPoolScanActive, parseScanStats } from '$lib/utils/zfs/pool';
	import { IsDocumentVisible, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent, ColumnDefinition } from 'tabulator-tables';

	interface Props {
		open: boolean;
		pool: Zpool;
		pools: Zpool[];
		initialSnapshot?: {
			hostname: string;
			poolGuid: string;
			status: ZpoolStatusPool | null;
			runs: PoolScrubRun[];
			schedules: PoolScrubSchedule[];
		};
	}

	let { open = $bindable(), pool, pools, initialSnapshot }: Props = $props();
	const visible = new IsDocumentVisible();
	let hostname = $derived(page.params.node || '');
	let selectedGUID = $derived(pool.pool_guid);
	let activeTab = $state('run');
	let status = $state<ZpoolStatusPool | null>(null);
	let runs = $state<PoolScrubRun[]>([]);
	let schedules = $state<PoolScrubSchedule[]>([]);
	let loading = $state(false);
	let refreshing = $state(false);
	let statusError = $state('');
	let historyError = $state('');
	let schedulesError = $state('');
	let action = $state<'start' | 'save' | 'disable' | 'delete' | ''>('');
	let startConfirmOpen = $state(false);
	let deleteConfirmOpen = $state(false);
	let deleteSchedule = $state<PoolScrubSchedule | null>(null);
	let formOpen = $state(false);
	let cronOpen = $state(false);
	let form = $state({
		id: null as number | null,
		cronExpr: '0 3 1 * *',
		enabled: true
	});
	let generation = 0;
	let requestSequence = 0;

	const scheduleOptions = [
		{ value: '0 3 1 * *', label: 'Monthly on day 1 at 03:00' },
		{ value: '0 3 * * 0', label: 'Weekly on Sunday at 03:00' }
	];

	function label(value: string): string {
		return (
			value.replaceAll('_', ' ').replace(/\b\w/g, (character) => character.toUpperCase()) ||
			'Unknown'
		);
	}

	function badgeClass(value: string): string {
		switch (value) {
			case 'starting':
			case 'running':
				return 'border-blue-500/40 bg-blue-500/10 text-blue-600 dark:text-blue-300';
			case 'passed':
				return 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300';
			case 'failed':
				return 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300';
			case 'queued':
			case 'paused':
			case 'cancelled':
			case 'unknown':
			case 'missed':
				return 'border-yellow-500/40 bg-yellow-500/10 text-yellow-700 dark:text-yellow-300';
			default:
				return 'text-muted-foreground';
		}
	}

	function localTime(value: string | null, fallback = 'Not reported'): string {
		return value ? convertDbTime(value) : fallback;
	}

	let selectedPool = $derived(
		pools.find((candidate) => candidate.pool_guid === selectedGUID) || null
	);
	let selectedName = $derived(selectedPool?.name || pool.name);
	let displayedSchedules = $derived(
		schedules.filter((schedule) => schedule.poolGuid === selectedGUID)
	);
	let enabledCount = $derived(displayedSchedules.filter((schedule) => schedule.enabled).length);
	let activeSchedule = $derived(
		displayedSchedules.find((schedule) => isPoolScrubActive(schedule.lastStatus))
	);
	let activeRun = $derived(runs.find((run) => isPoolScrubActive(run.status)) || null);
	let scanActive = $derived(isPoolScanActive(status?.scan_stats));
	let scanSummary = $derived(parseScanStats(status?.scan_stats));
	let scanPaused = $derived(
		scanActive &&
			(status?.scan_stats?.state?.toUpperCase() === 'PAUSED' ||
				Number(status?.scan_stats?.scrub_pause) > 0)
	);
	let currentProgress = $derived(
		scanActive
			? scanSummary.progressPercent
			: activeRun?.progressKnown
				? activeRun.progressPct
				: activeSchedule?.progressKnown
					? activeSchedule.progressPct
					: null
	);
	let progressTitle = $derived(
		scanActive && status?.scan_stats?.function?.toUpperCase() === 'RESILVER'
			? 'Resilver progress'
			: 'Scrub progress'
	);
	let running = $derived(scanActive || activeRun !== null || activeSchedule !== undefined);
	let hasLastScan = $derived(
		!running && !!scanSummary.text && status?.scan_stats?.function?.toUpperCase() !== 'NONE'
	);
	let scanPending = $derived(!status && !statusError && !running);
	let queued = $derived(
		displayedSchedules.some((schedule) => schedule.enabled && schedule.lastStatus === 'queued')
	);
	let executionStatus = $derived(
		scanActive
			? scanPaused
				? 'paused'
				: 'running'
			: activeRun?.status || activeSchedule?.lastStatus || (queued ? 'queued' : 'idle')
	);
	let startBlocked = $derived(
		!selectedPool || !status || !!statusError || running || queued || loading || action !== ''
	);
	let activeAnywhere = $derived(
		running || schedules.some((schedule) => isPoolScrubActive(schedule.lastStatus))
	);
	let editLocked = $derived(
		form.id !== null &&
			(schedules.find((schedule) => schedule.id === form.id)?.active || activeRun !== null)
	);
	let deleteLocked = $derived(
		deleteSchedule !== null &&
			(schedules.find((schedule) => schedule.id === deleteSchedule?.id)?.active ||
				activeRun !== null)
	);
	let cronDescription = $derived(cronToHuman(form.cronExpr));
	let invalidForm = $derived(
		!selectedPool || form.cronExpr.trim().split(/\s+/).length !== 5 || !cronDescription
	);

	function isCurrent(view: number, node: string, guid: string): boolean {
		return open && view === generation && node === hostname && guid === selectedGUID;
	}

	async function refresh(initial = false) {
		if (!open || (refreshing && !initial)) return;
		const view = generation;
		const node = hostname;
		const guid = selectedGUID;
		const request = ++requestSequence;
		refreshing = true;
		if (initial) loading = true;
		const options = { hostname: node };
		const statusRequest = (
			selectedPool ? getPoolStatusResult(guid, options) : Promise.resolve(null)
		).then((statusResponse) => {
			if (!isCurrent(view, node, guid) || request !== requestSequence) return;
			if (statusResponse && !isAPIResponse(statusResponse)) {
				status = statusResponse;
				statusError = '';
			} else if (statusResponse) {
				statusError = getAPIErrorText(statusResponse, 'Unable to read pool scan status');
			} else {
				status = null;
				statusError = 'This pool is unavailable or is no longer managed by Sylve.';
			}
		});
		const [scheduleResponse, runResponse] = await Promise.all([
			listPoolScrubSchedules(options),
			listPoolScrubRuns(guid, options),
			statusRequest
		]);
		if (!isCurrent(view, node, guid) || request !== requestSequence) return;
		if (Array.isArray(scheduleResponse)) {
			schedules = scheduleResponse;
			schedulesError = '';
		} else {
			schedulesError = getAPIErrorText(scheduleResponse, 'Unable to load scrub schedules');
		}
		if (Array.isArray(runResponse)) {
			runs = runResponse;
			historyError = '';
		} else if (runResponse) {
			historyError = getAPIErrorText(runResponse, 'Unable to load scrub history');
		}
		loading = false;
		refreshing = false;
	}

	function newSchedule() {
		if (!selectedPool) return;
		form = { id: null, cronExpr: '0 3 1 * *', enabled: true };
		cronOpen = false;
		formOpen = true;
	}

	function editSchedule(schedule: PoolScrubSchedule) {
		if (schedule.active) return;
		form = {
			id: schedule.id,
			cronExpr: schedule.cronExpr,
			enabled: schedule.enabled
		};
		cronOpen = false;
		formOpen = true;
	}

	async function saveSchedule() {
		if (action || invalidForm || editLocked) return;
		const view = generation;
		const node = hostname;
		const guid = selectedGUID;
		const id = form.id;
		const input = {
			poolGuid: guid,
			cronExpr: form.cronExpr.trim(),
			enabled: form.enabled
		};
		action = 'save';
		const response = id
			? await updatePoolScrubSchedule(id, input, { hostname: node })
			: await createPoolScrubSchedule(input, { hostname: node });
		if (!isCurrent(view, node, guid)) return;
		action = '';
		if (isAPIResponse(response)) {
			handleAPIError(response);
			toast.error(getAPIErrorText(response, 'Unable to save scrub schedule'), {
				position: 'bottom-center'
			});
			return;
		}
		formOpen = false;
		toast.success(id ? 'Scrub schedule updated' : 'Scrub schedule created', {
			position: 'bottom-center'
		});
		await refresh(true);
	}

	async function disableSchedule(schedule: PoolScrubSchedule) {
		if (action || schedule.active) return;
		const view = generation;
		const node = hostname;
		const guid = selectedGUID;
		action = 'disable';
		const response = await updatePoolScrubSchedule(
			schedule.id,
			{
				poolGuid: schedule.poolGuid,
				cronExpr: schedule.cronExpr,
				enabled: false
			},
			{ hostname: node }
		);
		if (!isCurrent(view, node, guid)) return;
		action = '';
		if (isAPIResponse(response)) {
			handleAPIError(response);
			toast.error(getAPIErrorText(response, 'Unable to disable scrub schedule'), {
				position: 'bottom-center'
			});
		} else {
			toast.success('Scrub schedule disabled', { position: 'bottom-center' });
		}
		await refresh(true);
	}

	async function confirmDelete() {
		if (!deleteSchedule || deleteLocked || action) return;
		const view = generation;
		const node = hostname;
		const guid = selectedGUID;
		action = 'delete';
		const response = await deletePoolScrubSchedule(deleteSchedule.id, { hostname: node });
		if (!isCurrent(view, node, guid)) return;
		action = '';
		deleteConfirmOpen = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error(getAPIErrorText(response, 'Unable to delete scrub schedule'), {
				position: 'bottom-center'
			});
		} else {
			deleteSchedule = null;
			toast.success('Scrub schedule deleted', { position: 'bottom-center' });
		}
		await refresh(true);
	}

	async function startScrub() {
		if (startBlocked) return;
		const view = generation;
		const node = hostname;
		const guid = selectedGUID;
		action = 'start';
		const response = await scrubPool(guid, { hostname: node });
		if (!isCurrent(view, node, guid)) return;
		action = '';
		startConfirmOpen = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error(getAPIErrorText(response, 'Unable to request pool scrub'), {
				position: 'bottom-center'
			});
		} else {
			toast.success('Scrub start requested; waiting for scan status', {
				position: 'bottom-center'
			});
		}
		await refresh(true);
	}

	function runDuration(run: PoolScrubRun): string {
		if (!run.startedAt || !run.completedAt) return '-';
		const minutes = Math.max(
			0,
			Math.floor((Date.parse(run.completedAt) - Date.parse(run.startedAt)) / 60000)
		);
		return minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
	}

	const historyColumns: ColumnDefinition[] = [
		{
			title: 'Date (Browser Local)',
			field: 'date',
			width: 190,
			minWidth: 190,
			sorter: 'number',
			formatter: (cell: CellComponent) => localTime(new Date(Number(cell.getValue())).toISOString())
		},
		{
			title: 'Source',
			field: 'source',
			width: 95,
			minWidth: 95,
			tooltip: true,
			formatter: 'plaintext'
		},
		{ title: 'Result', field: 'result', width: 100, minWidth: 100, formatter: 'plaintext' },
		{ title: 'Duration', field: 'duration', width: 85, minWidth: 85, formatter: 'plaintext' },
		{ title: 'Errors', field: 'errors', width: 70, minWidth: 70, formatter: 'plaintext' },
		{ title: 'Repaired', field: 'repaired', width: 90, minWidth: 90, formatter: 'plaintext' },
		{
			title: 'Details',
			field: 'details',
			width: 170,
			minWidth: 170,
			tooltip: true,
			formatter: 'plaintext'
		}
	];
	let historyRows = $derived(
		runs.map((run) => ({
			id: run.id,
			date: Date.parse(run.startedAt || run.requestedAt),
			source:
				run.scheduleId === null ? label(run.source) : `${label(run.source)} #${run.scheduleId}`,
			result: label(run.status),
			duration: runDuration(run),
			errors: run.errors ?? 'Not reported',
			repaired: run.repairedBytes === null ? 'Not reported' : formatBytesBinary(run.repairedBytes),
			details:
				run.error ||
				(run.examinedBytes !== null
					? `${formatBytesBinary(run.examinedBytes)} scanned`
					: 'Not reported')
		}))
	);

	watch([() => open, () => selectedGUID, () => hostname], ([isOpen]) => {
		generation++;
		refreshing = false;
		action = '';
		formOpen = false;
		cronOpen = false;
		startConfirmOpen = false;
		deleteConfirmOpen = false;
		deleteSchedule = null;
		if (!isOpen) return;
		activeTab = 'run';
		const snapshot =
			initialSnapshot &&
			initialSnapshot.hostname === hostname &&
			initialSnapshot.poolGuid === selectedGUID &&
			initialSnapshot.status?.pool_guid === selectedGUID
				? initialSnapshot
				: null;
		status = snapshot?.status || null;
		runs = snapshot?.runs || [];
		schedules = snapshot?.schedules || [];
		statusError = '';
		historyError = '';
		schedulesError = '';
		void refresh(true);
	});

	useInterval(5000, {
		callback: () => {
			if (open && visible.current && activeAnywhere && !action) void refresh();
		}
	});
	useInterval(30000, {
		callback: () => {
			if (open && visible.current && !activeAnywhere && !action) void refresh();
		}
	});
</script>

{#snippet progress(value: number | null, title = 'Scrub progress')}
	<div class="space-y-2">
		<div class="flex items-center justify-between gap-3 text-xs">
			<span class="font-medium">{title}</span>
			<span class="text-muted-foreground tabular-nums"
				>{value !== null ? `${value}%` : 'Progress not reported'}</span
			>
		</div>
		<div
			class="bg-secondary h-2 w-full overflow-hidden rounded-full"
			role="progressbar"
			aria-label={title}
			aria-valuemin="0"
			aria-valuemax="100"
			aria-valuenow={value ?? undefined}
			aria-valuetext={value !== null ? undefined : 'Progress not reported'}
		>
			{#if value !== null}
				<div
					class="h-full rounded-full bg-blue-500 transition-[width]"
					style="width: {Math.min(100, Math.max(0, value))}%"
				></div>
			{:else}
				<div
					class="h-full w-1/3 animate-pulse rounded-full bg-blue-500 motion-reduce:animate-none"
				></div>
			{/if}
		</div>
	</div>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] max-h-[600px] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden p-5 sm:h-[80dvh] sm:w-[calc(100vw-4rem)] sm:max-w-4xl"
	>
		<Dialog.Header class="shrink-0 pr-10 text-left">
			<Dialog.Title class="flex flex-wrap items-center gap-2">
				<SpanWithIcon
					icon="icon-[cil--scrubber]"
					size="h-5 w-5"
					gap="gap-2"
					title="ZFS Pool Scrub"
				/>
				{#if selectedName}<span class="text-muted-foreground">•</span><span>{selectedName}</span
					>{/if}
			</Dialog.Title>
			<Dialog.Description
				>Pool integrity scans, result history, and opt-in scheduling.</Dialog.Description
			>
		</Dialog.Header>

		<Tabs.Root bind:value={activeTab} class="mt-4 flex min-h-0 flex-1 flex-col">
			<Tabs.List class="grid h-10 w-full shrink-0 grid-cols-3 rounded-md">
				<Tabs.Trigger class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm" value="run"
					><span class="icon-[material-symbols--play-circle-outline] h-4 w-4 shrink-0"></span>Run
					Scrub</Tabs.Trigger
				>
				<Tabs.Trigger class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm" value="history"
					><span class="icon-[material-symbols--history] h-4 w-4 shrink-0"
					></span>History</Tabs.Trigger
				>
				<Tabs.Trigger class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm" value="schedule"
					><span class="icon-[material-symbols--calendar-clock-outline] h-4 w-4 shrink-0"
					></span>Schedule
					{#if enabledCount > 0}<Badge variant="outline" class="px-1.5 py-0 text-[10px]"
							>{enabledCount}</Badge
						>{/if}
				</Tabs.Trigger>
			</Tabs.List>
			<div class="flex min-h-0 flex-1 flex-col">
				<Tabs.Content
					value="run"
					class="m-0 flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto py-4"
				>
					{#if statusError}<div
							class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
						>
							{statusError}
						</div>{/if}
					<div
						class="flex min-h-44 shrink-0 flex-col overflow-hidden rounded-md border"
						class:flex-1={!scanPending && !running && !hasLastScan}
						aria-busy={scanPending}
					>
						<section
							aria-label="Pool scan status"
							class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b px-4 py-3 sm:px-5"
						>
							<div class="flex items-center gap-3">
								<span class="icon-[mdi--database-outline] text-muted-foreground h-5 w-5 shrink-0"
								></span>
								<dl class="space-y-0.5">
									<dt class="text-muted-foreground text-xs">Pool State</dt>
									<dd class="text-sm font-medium">
										{status?.state || selectedPool?.state || 'Unavailable'}
									</dd>
								</dl>
							</div>
							<div class="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-2">
								{#if activeRun || (scanActive && !loading)}
									<span class="text-muted-foreground text-xs">
										{activeRun ? label(activeRun.source) : 'External / untracked'}
									</span>
								{/if}
								{#if status || running || queued}
									<Badge variant="outline" class={badgeClass(executionStatus)}>
										{label(executionStatus)}
									</Badge>
								{:else}<span class="text-muted-foreground text-sm">-</span>{/if}
							</div>
						</section>
						{#if scanPending}
							<div class="h-24" aria-hidden="true"></div>
						{:else if running}
							<div class="space-y-4 p-4 sm:p-5">
								{@render progress(currentProgress, progressTitle)}
								{#if scanActive && scanSummary.text}
									<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
										{scanSummary.text}
									</p>
								{:else if executionStatus === 'starting'}
									<p class="text-muted-foreground text-xs">
										Waiting for ZFS to report the new scrub.
									</p>
								{/if}
								{#if activeRun?.error}<p class="text-xs text-yellow-700 dark:text-yellow-300">
										{activeRun.error}
									</p>{/if}
							</div>
						{:else if hasLastScan}
							<div class="space-y-3 p-4 sm:p-5">
								<p class="text-sm font-medium">
									{statusError ? 'Last observed scan' : 'Last scan'}
								</p>
								<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
									{scanSummary.text}
								</p>
								{#if queued}
									<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
										Disable queued schedules to cancel pending work before starting manually.
									</p>
								{/if}
							</div>
						{:else}
							<div
								class="flex min-h-40 flex-1 flex-col items-center justify-center gap-3 p-5 text-center"
							>
								<div
									class="bg-muted/40 text-muted-foreground flex h-11 w-11 items-center justify-center rounded-full"
								>
									<span class="icon-[cil--scrubber] h-6 w-6"></span>
								</div>
								<div class="max-w-sm space-y-1.5">
									<p class="text-sm font-medium">
										{queued
											? 'Scheduled scrub queued'
											: statusError
												? 'Scan status unavailable'
												: 'No scan in progress'}
									</p>
									<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
										{#if queued}
											Disable queued schedules to cancel pending work before starting manually.
										{:else}
											A scrub verifies stored data and repairs damage where redundancy permits.
										{/if}
									</p>
								</div>
							</div>
						{/if}
					</div>
				</Tabs.Content>

				<Tabs.Content
					value="history"
					class="m-0 flex min-h-0 flex-1 flex-col gap-3 overflow-hidden py-4"
				>
					{#if historyError}<div class="text-destructive rounded-md border p-3 text-sm">
							{historyError}. Last successfully loaded history is retained.<Button
								class="ml-2"
								size="sm"
								variant="outline"
								onclick={() => refresh()}>Retry</Button
							>
						</div>{/if}
					{#if loading && runs.length === 0}<p class="text-muted-foreground">
							Loading scrub history
						</p>
					{:else if runs.length > 0}
						<ModalTable
							name="mt-zfsPoolScrubHistory"
							rows={historyRows}
							columns={historyColumns}
							initialSort={[{ column: 'date', dir: 'desc' }]}
							placeholder="No tracked scrubs are available."
						/>
					{:else if !historyError}
						<div
							class="flex min-h-44 flex-1 flex-col items-center justify-center gap-3 rounded-md border p-5 text-center"
						>
							<div
								class="bg-muted/40 text-muted-foreground flex h-11 w-11 items-center justify-center rounded-full"
							>
								<span class="icon-[material-symbols--history] h-6 w-6"></span>
							</div>
							<div class="max-w-sm space-y-1.5">
								<p class="text-sm font-medium">No scrub history</p>
								<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
									Scrub runs and skipped scheduled occurrences in Sylve will appear here.
								</p>
							</div>
						</div>
					{/if}
				</Tabs.Content>

				<Tabs.Content
					value="schedule"
					class="m-0 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto py-4"
				>
					{#if schedulesError}<div
							class="border-yellow-500/40 bg-yellow-500/10 rounded-md border p-3 text-sm text-yellow-700 dark:text-yellow-300"
						>
							{schedulesError}. Last successfully loaded schedules are retained.<Button
								class="ml-2"
								size="sm"
								variant="outline"
								onclick={() => refresh()}>Retry</Button
							>
						</div>{/if}
					{#if formOpen}
						<section class="shrink-0 space-y-5 rounded-md border p-4 sm:p-5">
							<div class="space-y-1">
								<p class="text-sm font-medium">
									{form.id ? `Edit Schedule #${form.id}` : 'New Schedule'}
								</p>
								<p class="text-muted-foreground text-xs">
									Run automatically using the node's local time. Occurrences overlapping a scrub or
									within 24 hours after its completion are skipped, not postponed.
								</p>
							</div>
							{#if editLocked}<p class="text-sm text-yellow-700 dark:text-yellow-300">
									This pool has an active scrub. Its schedules cannot be edited until the run
									finishes.
								</p>{/if}
							<div class="min-w-0 space-y-1">
								<p class="text-sm font-medium">Schedule</p>
								<CustomComboBox
									bind:open={cronOpen}
									bind:value={form.cronExpr}
									data={scheduleOptions}
									placeholder="Select or enter a cron expression"
									width="w-full"
									allowCustom={true}
									disallowEmpty={true}
									disabled={editLocked || action !== ''}
									classes=""
								/>
							</div>
							<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
								<p class="text-sm">{cronDescription || 'Invalid cron expression'}</p>
								<CustomCheckbox
									label="Enabled"
									bind:checked={form.enabled}
									disabled={editLocked || action !== '' || !selectedPool}
								/>
							</div>
						</section>
					{:else if loading && schedules.length === 0}<p class="text-muted-foreground">
							Loading schedules
						</p>
					{:else if displayedSchedules.length === 0 && !schedulesError}
						<div
							class="flex min-h-44 flex-1 flex-col items-center justify-center gap-3 rounded-md border p-5 text-center"
						>
							<div
								class="bg-muted/40 text-muted-foreground flex h-11 w-11 items-center justify-center rounded-full"
							>
								<span class="icon-[material-symbols--calendar-clock-outline] h-6 w-6"></span>
							</div>
							<div class="max-w-sm space-y-1.5">
								<p class="text-sm font-medium">No schedule configured</p>
								<p class="text-muted-foreground text-xs leading-relaxed text-pretty">
									Create a weekly or monthly scrub schedule for this pool, or enter a custom cron
									expression.
								</p>
							</div>
						</div>
					{:else}
						{#each displayedSchedules as schedule (schedule.id)}
							<div class="shrink-0 space-y-4 rounded-md border p-4 sm:p-5">
								<div class="flex flex-wrap items-center justify-between gap-2">
									<div class="flex flex-wrap items-center gap-2">
										<span class="text-sm font-medium">Periodic Scrub #{schedule.id}</span><Badge
											variant="outline"
											class={badgeClass(schedule.lastStatus)}>{label(schedule.lastStatus)}</Badge
										><Badge
											variant="outline"
											class={schedule.enabled
												? 'text-green-600 dark:text-green-300'
												: 'text-muted-foreground'}
											>{schedule.enabled ? 'Enabled' : 'Disabled'}</Badge
										>{#if !selectedPool}<Badge variant="outline">Unavailable Pool</Badge>{/if}
									</div>
									<div class="flex items-center gap-1">
										{#if !selectedPool && schedule.enabled}<Button
												size="sm"
												variant="outline"
												disabled={schedule.active || action !== ''}
												onclick={() => disableSchedule(schedule)}>Disable</Button
											>{/if}
										<Button
											size="sm"
											variant="ghost"
											disabled={action !== ''}
											onclick={() => {
												activeTab = 'history';
											}}
											title="View history"><span class="icon-[mdi--history] h-4 w-4"></span></Button
										>
										<Button
											size="sm"
											variant="ghost"
											disabled={schedule.active || action !== '' || !selectedPool}
											onclick={() => editSchedule(schedule)}
											title="Edit schedule"><span class="icon-[mdi--pencil] h-4 w-4"></span></Button
										>
										<Button
											size="sm"
											variant="ghost"
											disabled={schedule.active || action !== ''}
											onclick={() => {
												deleteSchedule = schedule;
												deleteConfirmOpen = true;
											}}
											title="Delete schedule"
											><span class="icon-[mdi--delete-outline] h-4 w-4"></span></Button
										>
									</div>
								</div>
								<div class="grid gap-3 text-sm sm:grid-cols-3">
									<div>
										<p class="text-muted-foreground text-xs">Schedule (Node Local)</p>
										<p title={schedule.cronExpr}>
											{cronToHuman(schedule.cronExpr) || schedule.cronExpr}
										</p>
									</div>
									<div>
										<p class="text-muted-foreground text-xs">Next Run (Browser Local)</p>
										<p>
											{schedule.enabled
												? localTime(schedule.nextRunAt, 'Not scheduled')
												: 'Disabled'}
										</p>
									</div>
									<div>
										<p class="text-muted-foreground text-xs">Last Run (Browser Local)</p>
										<p>{localTime(schedule.lastRunAt, 'Never')}</p>
									</div>
								</div>
								{#if schedule.lastStatus === 'queued'}<p class="text-muted-foreground text-xs">
										Queued {localTime(schedule.queuedAt)} browser local; waiting for the scheduled slot
										or for a resilver. New occurrences are coalesced while waiting.
									</p>{/if}
								{#if isPoolScrubActive(schedule.lastStatus)}{@render progress(
										currentProgress,
										progressTitle
									)}{/if}
								{#if schedule.lastError}<div
										class={schedule.lastStatus === 'skipped'
											? 'text-muted-foreground rounded-md border p-2 text-xs'
											: 'border-destructive/30 bg-destructive/5 text-destructive rounded-md border p-2 text-xs'}
									>
										{schedule.lastError}
									</div>{/if}
								{#if schedule.active}<p class="text-muted-foreground text-xs">
										This pool has an active scrub; its schedules cannot be edited or deleted.
									</p>{/if}
							</div>
						{/each}
					{/if}
				</Tabs.Content>
			</div>
		</Tabs.Root>
		<Dialog.Footer class="h-14 shrink-0 flex-row items-center justify-end pt-3">
			{#if activeTab === 'schedule' && formOpen}
				<Button
					size="sm"
					variant="outline"
					disabled={action !== ''}
					onclick={() => (formOpen = false)}>Back</Button
				>
				<Button
					size="sm"
					disabled={invalidForm || editLocked || action !== ''}
					onclick={saveSchedule}
				>
					{#if action === 'save'}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>{/if}
					Save Schedule
				</Button>
			{:else if activeTab === 'run'}
				<Button size="sm" disabled={startBlocked} onclick={() => (startConfirmOpen = true)}>
					<SpanWithIcon
						icon="icon-[material-symbols--play-circle-outline]"
						size="h-4 w-4"
						gap="gap-2"
						title="Start Scrub"
					/>
				</Button>
			{:else if activeTab === 'schedule' && selectedPool}
				<Button
					size="sm"
					disabled={action !== '' || loading || !!schedulesError}
					onclick={newSchedule}
				>
					<span class="icon-[gg--add] h-4 w-4"></span>Add Schedule
				</Button>
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={startConfirmOpen}>
	<AlertDialog.Content
		class="p-5"
		onInteractOutside={(event) => event.preventDefault()}
		onEscapeKeydown={(event) => {
			if (action) event.preventDefault();
		}}
	>
		<AlertDialog.Header
			><AlertDialog.Title>Start scrub on {selectedName}?</AlertDialog.Title><AlertDialog.Description
				>The pool remains online, but a scrub can substantially reduce <span
					class="whitespace-nowrap">I/O</span
				> performance. It may take hours or days. Closing this window does not stop it.</AlertDialog.Description
			></AlertDialog.Header
		>
		<AlertDialog.Footer
			><AlertDialog.Cancel disabled={action !== ''}>Cancel</AlertDialog.Cancel><AlertDialog.Action
				disabled={startBlocked}
				onclick={(event) => {
					event.preventDefault();
					void startScrub();
				}}>{action === 'start' ? 'Starting' : 'Start Scrub'}</AlertDialog.Action
			></AlertDialog.Footer
		>
	</AlertDialog.Content>
</AlertDialog.Root>

<AlertDialog.Root bind:open={deleteConfirmOpen}>
	<AlertDialog.Content
		class="p-5"
		onInteractOutside={(event) => event.preventDefault()}
		onEscapeKeydown={(event) => {
			if (action) event.preventDefault();
		}}
	>
		<AlertDialog.Header
			><AlertDialog.Title>Delete scrub schedule?</AlertDialog.Title><AlertDialog.Description
				>{deleteLocked
					? 'This pool has an active scrub. Wait for it to finish before deleting the schedule.'
					: `Schedule #${deleteSchedule?.id} for ${deleteSchedule?.poolName || ''} and its queued occurrence will be removed. Other schedules and completed history are retained.`}</AlertDialog.Description
			></AlertDialog.Header
		>
		<AlertDialog.Footer
			><AlertDialog.Cancel disabled={action !== ''}>Cancel</AlertDialog.Cancel><AlertDialog.Action
				class="bg-destructive text-white hover:bg-destructive/90"
				disabled={deleteLocked || action !== ''}
				onclick={(event) => {
					event.preventDefault();
					void confirmDelete();
				}}>{action === 'delete' ? 'Deleting' : 'Delete Schedule'}</AlertDialog.Action
			></AlertDialog.Footer
		>
	</AlertDialog.Content>
</AlertDialog.Root>
