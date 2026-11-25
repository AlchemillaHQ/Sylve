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
		abortSmartSelfTest,
		getSmartSelfTest,
		listSmartSelfTestSchedules,
		startSmartSelfTest
	} from '$lib/api/disk/disk';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import type {
		Disk,
		SmartSelfTestDetails,
		SmartSelfTestResult,
		SmartSelfTestSchedule,
		SmartSelfTestStartKind
	} from '$lib/types/disk/disk';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import { useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { diskActionError } from './disks';
	import Schedules from './Schedules.svelte';
	import {
		durationMinutes,
		formatDuration,
		memorySnapshot,
		progressPercent,
		resultDate,
		resultDetail,
		resultHours,
		resultLocation,
		resultOutcome,
		resultTone,
		saveSnapshot,
		scheduleActive,
		storedSnapshot,
		testKinds,
		testLabel,
		type ResultTone
	} from './selftest';

	let { disk, hostname, canModify }: { disk: Disk; hostname: string; canModify: boolean } =
		$props();

	const id = $props.id();
	const lifetime = new AbortController();
	// svelte-ignore state_referenced_locally
	const target = { hostname, device: disk.device, uuid: disk.uuid };
	const cached = memorySnapshot(target.hostname, target.device, target.uuid);
	let details = $state<SmartSelfTestDetails | null>(cached?.details ?? null);
	let schedules = $state<SmartSelfTestSchedule[]>(cached?.schedules ?? []);
	let loading = $state(!cached);
	let showLoading = $state(false);
	let refreshing = $state(false);
	let unsupported = $state(false);
	let loadError = $state('');
	let refreshFailed = $state(false);
	let schedulesStale = $state(false);
	let readAt = $state<number | null>(cached?.readAt ?? null);
	let selected = $state<SmartSelfTestStartKind | ''>(defaultTest(cached?.details));
	let starting = $state(false);
	let aborting = $state(false);
	let actionError = $state('');
	let abortOpen = $state(false);

	let virtual = $derived(disk.type === 'Virtual');
	let diskSchedules = $derived(schedules.filter((schedule) => schedule.diskKey === disk.uuid));
	let activeSchedule = $derived(diskSchedules.find(scheduleActive));
	let queued = $derived(diskSchedules.some((schedule) => schedule.lastStatus === 'queued'));
	let deviceBusy = $derived(
		details?.status.running === true || details?.status.state === 'ambiguous'
	);
	let running = $derived(deviceBusy || activeSchedule !== undefined);
	let available = $derived(testKinds.filter((test) => details?.capabilities[test.kind] === true));
	let runningType = $derived(activeSchedule?.testType || details?.status.type || '');
	let progress = $derived(progressPercent(details, activeSchedule));
	let estimate = $derived(
		activeSchedule?.estimatedMinutes ||
			details?.status.estimated_duration_minutes ||
			durationMinutes(runningType, details?.capabilities)
	);
	let canAbort = $derived(canModify && deviceBusy && details?.capabilities.abort === true);
	let selectedLabel = $derived(selected ? testLabel(selected) : '');

	function defaultTest(
		info?: SmartSelfTestDetails | null,
		current = ''
	): SmartSelfTestStartKind | '' {
		const kinds = testKinds.filter((test) => info?.capabilities[test.kind] === true);
		return kinds.find((test) => test.kind === current)?.kind ?? kinds[0]?.kind ?? '';
	}

	function scheduleSaved(schedule: SmartSelfTestSchedule) {
		const index = schedules.findIndex((item) => item.id === schedule.id);
		schedules = index >= 0 ? schedules.with(index, schedule) : [...schedules, schedule];
		remember();
	}

	function scheduleDeleted(scheduleId: number) {
		schedules = schedules.filter((item) => item.id !== scheduleId);
		remember();
	}

	function remember() {
		if (details && readAt)
			saveSnapshot(target.hostname, target.device, target.uuid, { details, schedules, readAt });
	}

	async function load(withDetails = true) {
		if (virtual || refreshing || lifetime.signal.aborted) return;
		refreshing = true;
		const options = { hostname, signal: lifetime.signal };
		try {
			const [info, list] = await Promise.all([
				withDetails ? getSmartSelfTest(disk.device, options) : Promise.resolve(null),
				listSmartSelfTestSchedules(options)
			]);
			if (lifetime.signal.aborted) return;
			if (Array.isArray(list)) schedules = list;
			schedulesStale = !Array.isArray(list);
			if (!info) {
				remember();
				return;
			}
			if (isAPIResponse(info)) {
				if (info.message === 'smart_self_test_unsupported') unsupported = true;
				else if (details) refreshFailed = true;
				else loadError = diskActionError(info, 'Could not read the self-test status.');
				return;
			}
			details = info;
			unsupported = false;
			loadError = '';
			refreshFailed = false;
			readAt = Date.now();
			selected = defaultTest(info, selected);
			remember();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) {
				if (details) refreshFailed = true;
				else loadError = 'Could not read the self-test status.';
			}
		} finally {
			if (!lifetime.signal.aborted) {
				refreshing = false;
				loading = false;
			}
		}
	}

	async function start() {
		if (!selected || starting || running || queued || !canModify) return;
		const kind = selected;
		starting = true;
		actionError = '';
		try {
			const response = await startSmartSelfTest(disk.device, kind, { hostname });
			if (lifetime.signal.aborted) return;
			if (isAPIResponse(response)) {
				actionError = diskActionError(response, 'Could not start the test. Please try again.');
				handleAPIError(response);
			} else {
				details = response;
				readAt = Date.now();
				remember();
				toast.success(`${testLabel(kind)} test started on ${disk.device}`, {
					position: 'bottom-center'
				});
			}
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) {
				actionError = 'Could not start the test. Please try again.';
			}
		} finally {
			if (!lifetime.signal.aborted) starting = false;
		}
		void load();
	}

	async function abort() {
		if (aborting || !canAbort) return;
		aborting = true;
		actionError = '';
		try {
			const response = await abortSmartSelfTest(disk.device, { hostname });
			if (lifetime.signal.aborted) return;
			if (isAPIResponse(response)) {
				actionError = diskActionError(response, 'Could not stop the test. Please try again.');
				handleAPIError(response);
			} else {
				details = response;
				readAt = Date.now();
				remember();
				abortOpen = false;
				toast.success(`Test stopped on ${disk.device}`, { position: 'bottom-center' });
			}
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) {
				actionError = 'Could not stop the test. Please try again.';
			}
		} finally {
			if (!lifetime.signal.aborted) aborting = false;
		}
		void load();
	}

	void load();
	if (!cached) {
		const spinner = setTimeout(() => (showLoading = true), 250);
		lifetime.signal.addEventListener('abort', () => clearTimeout(spinner));
		void storedSnapshot(target.hostname, target.device, target.uuid).then((stored) => {
			if (!stored || details || lifetime.signal.aborted) return;
			details = stored.details;
			schedules = stored.schedules;
			readAt = stored.readAt;
			selected = defaultTest(stored.details, selected);
			loading = false;
		});
	}

	const interval = useInterval(() => (running ? 5_000 : 30_000), {
		callback: () => {
			if (storage.visible && !loading && !starting && !aborting) void load(running);
		}
	});
	watch(
		() => storage.visible,
		(visible) => {
			if (visible && !loading) void load(running);
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const tone: Record<ResultTone, { dot: string; text: string }> = {
		passed: { dot: 'bg-emerald-500', text: '' },
		failed: { dot: 'bg-red-500', text: 'text-red-700 dark:text-red-400' },
		aborted: { dot: 'bg-amber-500', text: 'text-amber-700 dark:text-amber-400' },
		running: { dot: 'bg-sky-500 animate-pulse', text: '' },
		unknown: { dot: 'bg-muted-foreground/50', text: 'text-muted-foreground' }
	};

	type ResultRow = { key: string; result: SmartSelfTestResult };
	let rows = $derived(
		(details?.status.results ?? []).map((result, index) => ({ key: String(index), result }))
	);
	const helper = createColumnHelper<DataTableFeatures, ResultRow>();
	const columns = helper.columns([
		helper.accessor((row) => resultDate(row.result), {
			id: 'date',
			header: 'Started',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(dateCell, row.original.result)
		}),
		helper.accessor((row) => testLabel(row.result.type), { id: 'type', header: 'Type' }),
		helper.accessor((row) => resultOutcome(row.result), {
			id: 'result',
			header: 'Result',
			cell: ({ row }) => renderSnippet(outcomeCell, row.original.result)
		}),
		helper.accessor((row) => Number(resultHours(row.result) ?? -1) || 0, {
			id: 'hours',
			header: 'Drive Hours',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(hoursCell, row.original.result)
		}),
		helper.accessor((row) => resultLocation(row.result), { id: 'location', header: 'First Error' }),
		helper.accessor((row) => resultDetail(row.result), { id: 'details', header: 'Details' })
	]);
</script>

{#snippet dateCell(result: SmartSelfTestResult)}
	{@const date = resultDate(result)}
	<span class="whitespace-nowrap {date < 0 ? 'text-muted-foreground' : ''}">
		{date < 0 ? 'Not Recorded' : convertDbTime(new Date(date).toISOString())}
	</span>
{/snippet}

{#snippet outcomeCell(result: SmartSelfTestResult)}
	{@const kind = resultTone(result)}
	<span class="whitespace-nowrap {tone[kind].text}">
		<span
			class="mr-1.5 inline-block size-1.5 rounded-full align-middle {tone[kind].dot}"
			aria-hidden="true"
		></span>{resultOutcome(result)}
	</span>
{/snippet}

{#snippet hoursCell(result: SmartSelfTestResult)}
	{@const hours = resultHours(result)}
	{#if hours === null}
		<span class="text-muted-foreground" title="Recorded when the test finishes">—</span>
	{:else}
		<span class="whitespace-nowrap tabular-nums">{hours} h</span>
	{/if}
{/snippet}

{#snippet resultCard(row: Row<DataTableFeatures, ResultRow>)}
	{@const result = row.original.result}
	<div class="flex flex-wrap items-baseline justify-between gap-2 text-sm">
		<span class="font-medium">{testLabel(result.type)}</span>
		<span class="text-xs">{@render outcomeCell(result)}</span>
	</div>
	<p class="text-muted-foreground mt-1 text-xs">
		{@render dateCell(result)} · {@render hoursCell(result)}
	</p>
	<p class="text-muted-foreground mt-1.5 text-xs leading-5 break-words">
		First error: <span class="text-foreground">{resultLocation(result)}</span> · {resultDetail(
			result
		)}
	</p>
{/snippet}

{#snippet notice(text: string)}
	<div
		class="text-muted-foreground rounded-md border border-dashed px-4 py-6 text-center text-sm leading-6"
		role="status"
	>
		{text}
	</div>
{/snippet}

<div class="space-y-4">
	{#if virtual}
		{@render notice('Self-tests are not available for virtual disks.')}
	{:else if loading && !details}
		{#if showLoading}
			<div
				class="text-muted-foreground rounded-md border border-dashed px-4 py-6 text-center text-sm"
				role="status"
			>
				<span class="inline-flex items-center gap-2">
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
					></span>Reading self-test status…
				</span>
			</div>
		{/if}
	{:else if unsupported}
		{@render notice('This disk does not support S.M.A.R.T. self-tests.')}
	{:else if !details}
		<div
			class="rounded-md border border-dashed px-4 py-6 text-center text-sm leading-6"
			role="alert"
		>
			<p class="text-muted-foreground">{loadError || 'Could not read the self-test status.'}</p>
			<Button variant="outline" size="sm" class="mt-3" disabled={refreshing} onclick={() => load()}
				>Try Again</Button
			>
		</div>
	{:else}
		{#if running || queued}
			<section class="rounded-md border" aria-label="Self-test status">
				<div class="border-b px-4 py-3">
					<p class="flex items-center gap-2 text-sm font-medium">
						<span
							class="size-4 shrink-0 {running
								? 'icon-[lucide--loader-circle] text-sky-600 animate-spin dark:text-sky-400'
								: 'icon-[lucide--clock] text-amber-600 dark:text-amber-400'}"
							aria-hidden="true"
						></span>
						{#if details.status.state === 'ambiguous' && !details.status.running}
							Test status unclear
						{:else if running}
							{runningType ? `${testLabel(runningType)} test` : 'Test'} running{activeSchedule
								? ' (scheduled)'
								: ''}
						{:else}
							Scheduled test queued
						{/if}
					</p>
				</div>
				<div class="space-y-3 p-4 text-sm">
					{#if running}
						<div class="space-y-1.5">
							<div class="flex justify-between gap-3 text-xs">
								<span class="text-muted-foreground">
									{estimate > 0 ? `${formatDuration(estimate)} in total` : formatDuration(estimate)}
								</span>
								<span class="tabular-nums">
									{progress === null
										? details.capabilities.progress
											? 'Waiting for the drive'
											: 'Progress not reported'
										: `${progress}%`}
								</span>
							</div>
							<div
								class="bg-muted h-2 overflow-hidden rounded-full"
								role="progressbar"
								aria-label="Self-test progress"
								aria-valuemin="0"
								aria-valuemax="100"
								aria-valuenow={progress ?? undefined}
							>
								{#if progress !== null}
									<div
										class="h-full rounded-full bg-sky-500 transition-[width]"
										style:width="{progress}%"
									></div>
								{/if}
							</div>
						</div>
						{#if details.status.state === 'ambiguous' && !details.status.running}
							<p class="text-muted-foreground text-xs leading-5">
								The drive does not say whether a test is still running, so new tests stay blocked
								until it does.
							</p>
						{/if}
						<div class="flex flex-wrap items-center justify-between gap-3">
							<p class="text-muted-foreground text-xs leading-5">
								The test runs on the drive itself and keeps going if you close this window.
							</p>
							{#if canAbort}
								<Button
									variant="outline"
									size="sm"
									class="text-destructive h-8"
									disabled={aborting}
									onclick={() => (abortOpen = true)}
								>
									Stop Test
								</Button>
							{/if}
						</div>
					{:else}
						<p class="text-muted-foreground text-xs leading-5">
							A scheduled test is waiting to start. Manual tests are available again once it has
							run.
						</p>
					{/if}
				</div>
			</section>
		{/if}

		{#if canModify && !running}
			<section class="space-y-3" aria-labelledby="{id}-run">
				<h3 id="{id}-run" class="text-sm font-medium">Run a Test</h3>
				{#if !details.capabilities.supported || available.length === 0}
					<p class="text-muted-foreground text-sm">
						This drive does not offer any self-tests that can run in the background.
					</p>
				{:else}
					<div
						class="grid grid-cols-[repeat(auto-fit,minmax(12rem,1fr))] gap-2"
						role="radiogroup"
						aria-labelledby="{id}-run"
					>
						{#each available as test (test.kind)}
							<label
								class="has-checked:border-zen-accent has-checked:bg-zen-highlight/40 has-focus-visible:ring-ring flex cursor-pointer flex-col gap-1 rounded-md border p-3 has-focus-visible:ring-2 has-disabled:cursor-not-allowed has-disabled:opacity-60"
							>
								<input
									type="radio"
									class="sr-only"
									name="{id}-test"
									value={test.kind}
									bind:group={selected}
									disabled={starting || queued}
								/>
								<span class="flex items-baseline justify-between gap-2 text-sm font-medium">
									{test.label}
									<span class="text-muted-foreground text-xs font-normal tabular-nums">
										{formatDuration(durationMinutes(test.kind, details.capabilities))}
									</span>
								</span>
								<span class="text-muted-foreground text-xs leading-5">{test.description}</span>
							</label>
						{/each}
					</div>
					{#if details.capabilities.protocol === 'SCSI' && !details.capabilities.execution_support_known}
						<p class="text-muted-foreground text-xs leading-5">
							This drive does not confirm self-test support, so it may refuse to start one.
						</p>
					{/if}
					<div class="flex flex-wrap items-center justify-between gap-3">
						<p class="text-muted-foreground text-xs leading-5">
							The disk stays online but may be slower while the test runs.
						</p>
						<Button
							size="sm"
							class="ml-auto h-9 gap-2"
							disabled={!selected || starting || queued}
							onclick={start}
						>
							{#if starting}
								<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
								></span>
							{/if}
							{starting ? 'Starting…' : `Start ${selectedLabel} Test`}
						</Button>
					</div>
				{/if}
			</section>
		{/if}
		{#if actionError}<p class="text-destructive text-sm" role="alert">{actionError}</p>{/if}

		<section class="space-y-3" aria-labelledby="{id}-results">
			<div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
				<h3 id="{id}-results" class="text-sm font-medium">Results</h3>
				<div class="flex items-center gap-2">
					{#if readAt}
						<p class="text-muted-foreground text-xs" role="status">
							Read {new Date(readAt).toLocaleTimeString()}{refreshFailed
								? ' · Latest refresh failed'
								: ''}
						</p>
					{/if}
					<Button
						variant="ghost"
						size="icon"
						class="-my-1.5 -mr-2 size-8"
						disabled={refreshing || starting || aborting}
						onclick={() => load()}
						aria-label="Refresh self-test status"
					>
						<span
							class="icon-[lucide--refresh-cw] size-4"
							class:animate-spin={refreshing}
							aria-hidden="true"
						></span>
					</Button>
				</div>
			</div>
			{#if details.capabilities.protocol === 'SCSI' && !details.capabilities.result_log}
				{@render notice(
					'This drive does not keep a self-test log, so past results are not available.'
				)}
			{:else}
				{#if details.capabilities.result_log && !details.status.checksum_valid}
					<p class="text-xs leading-5 text-amber-700 dark:text-amber-400" role="status">
						The drive’s self-test log failed its checksum, so these results may be incomplete or
						wrong.
					</p>
				{/if}
				<DataTable
					data={rows}
					{columns}
					getRowId={(row) => row.key}
					getRowLabel={(row) => `${testLabel(row.result.type)} ${resultOutcome(row.result)}`}
					initialPageSize={5}
					pageSizeOptions={[5, 10, 25]}
					searchPlaceholder="Search results…"
					fullWidthSearch
					emptyMessage="No self-test results are recorded on this drive."
					columnClasses={{ hours: 'pr-6 text-right' }}
					mobileRow={resultCard}
				/>
			{/if}
		</section>

		<Schedules
			{disk}
			{hostname}
			{canModify}
			capabilities={details.capabilities}
			schedules={diskSchedules}
			stale={schedulesStale}
			onSaved={scheduleSaved}
			onDeleted={scheduleDeleted}
		/>
	{/if}
</div>

<AlertDialog.Root bind:open={abortOpen}>
	<AlertDialog.Content
		class="z-60 flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		overlayClass="z-60"
		escapeKeydownBehavior={aborting ? 'ignore' : 'close'}
		aria-busy={aborting}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="icon-[lucide--circle-stop] text-destructive size-5 shrink-0"
						aria-hidden="true"
					></span>
					Stop Test?
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={aborting}>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={aborting}
					>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				This stops the {runningType ? `${testLabel(runningType)} ` : ''}test on
				<span class="text-foreground text-sm font-semibold break-all">{disk.device}</span>
				on <span class="text-foreground text-sm font-semibold break-all">{hostname}</span>. It
				cannot be resumed; a new test starts from the beginning.
			</AlertDialog.Description>
			{#if actionError}<p class="text-destructive text-sm" role="alert">{actionError}</p>{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={aborting || !canAbort} onclick={abort}>
				{#if aborting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}
				{aborting ? 'Stopping…' : 'Stop Test'}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
