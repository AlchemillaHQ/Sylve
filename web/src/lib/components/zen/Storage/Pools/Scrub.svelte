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
		createPoolScrubSchedule,
		deletePoolScrubSchedule,
		listPoolScrubRuns,
		listPoolScrubSchedules,
		scrubPool,
		updatePoolScrubSchedule
	} from '$lib/api/zfs/pool';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { allowed, canReadHost, nodePath, permissions } from '$lib/stores/permissions.svelte';
	import {
		isPoolScrubActive,
		PoolScrubRunSchema,
		PoolScrubScheduleSchema,
		type PoolScrubRun,
		type PoolScrubSchedule,
		type Zpool,
		type ZpoolStatusPool
	} from '$lib/types/zfs/pool';
	import { SEVEN_DAYS } from '$lib/utils';
	import {
		APIRequestError,
		cachedFetch,
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		removeCache,
		updateCache
	} from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { isPoolScanActive } from '$lib/utils/zfs/pool';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import ScanActivity from './ScanActivity.svelte';
	import ScrubHistory from './ScrubHistory.svelte';
	import {
		scanDate,
		scrubDot,
		scrubLabel,
		scrubRunsCacheKey,
		scrubSchedulesCacheKey,
		scrubTone
	} from './scan';

	type ScrubMetadata = { schedules: PoolScrubSchedule[]; runs: PoolScrubRun[] };
	type ScrubSnapshot = ScrubMetadata & { status: ZpoolStatusPool };

	let {
		hostname,
		pool,
		status,
		statusLoading,
		statusError,
		refreshStatus,
		refreshVersion = 0,
		active = $bindable(false)
	}: {
		hostname: string;
		pool: Zpool;
		status: ZpoolStatusPool | null;
		statusLoading: boolean;
		statusError: boolean;
		refreshStatus: () => Promise<ZpoolStatusPool | null | undefined>;
		refreshVersion?: number;
		active?: boolean;
	} = $props();
	const identity = untrack(() => ({ hostname, guid: pool.guid }));
	const id = $props.id();
	const lifetime = new AbortController();
	let requests = new AbortController();
	let metadataRequest: Promise<ScrubMetadata | null> | null = null;
	let revision = 0;
	let priming = $state(true);
	let metadataLoading = $state(false);
	let validatedSchedules = $state(false);
	let validatedRuns = $state(false);
	let tab = $state('activity');
	let action = $state<'start' | 'save' | 'toggle' | 'delete' | ''>('');
	let requestedAfter = $state<number | null>(null);
	let formOpen = $state(false);
	let formError = $state('');
	let mutationError = $state('');
	let confirmOpen = $state(false);
	let confirmation = $state<'start' | 'delete'>('start');
	let deleteTarget = $state<PoolScrubSchedule | null>(null);
	let draft = $state({ id: null as number | null, cronExpr: '0 3 1 * *', enabled: true });

	function currentRequest(signal: AbortSignal) {
		const token = storage.token;
		const grants = permissions.data;
		return () => {
			signal.throwIfAborted();
			if (token !== storage.token || grants !== permissions.data) {
				throw new DOMException('Scrub access changed.', 'AbortError');
			}
		};
	}

	const schedules = resource(
		() => identity.hostname,
		async (node, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal, requests.signal]);
			const check = currentRequest(requestSignal);
			try {
				check();
				const result = await listPoolScrubSchedules({ hostname: node, signal: requestSignal });
				check();
				if (isAPIResponse(result)) throw new APIRequestError(result);
				await updateCache(scrubSchedulesCacheKey, result, node);
				check();
				validatedSchedules = true;
				return result;
			} catch (error) {
				check();
				validatedSchedules = false;
				throw error;
			}
		},
		{ initialValue: [] as PoolScrubSchedule[], lazy: true }
	);
	const history = resource(
		() => identity.guid,
		async (guid, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal, requests.signal]);
			const check = currentRequest(requestSignal);
			try {
				check();
				const result = await listPoolScrubRuns(guid, {
					hostname: identity.hostname,
					signal: requestSignal
				});
				check();
				if (isAPIResponse(result)) throw new APIRequestError(result);
				if (result.some((run) => run.poolGuid !== guid))
					throw new Error('Scrub history did not match the selected pool.');
				await updateCache(scrubRunsCacheKey(guid), result, identity.hostname);
				check();
				validatedRuns = true;
				return result;
			} catch (error) {
				check();
				validatedRuns = false;
				throw error;
			}
		},
		{ initialValue: [] as PoolScrubRun[], lazy: true }
	);

	let displayedSchedules = $derived(
		schedules.current.filter((schedule) => schedule.poolGuid === identity.guid)
	);
	let activeRun = $derived(history.current.find((run) => isPoolScrubActive(run.status)) ?? null);
	let activeSchedule = $derived(
		displayedSchedules.find((schedule) => isPoolScrubActive(schedule.lastStatus)) ??
			displayedSchedules.find((schedule) => schedule.active) ??
			null
	);
	let scanActive = $derived(isPoolScanActive(status?.scan_stats));
	let queued = $derived(
		displayedSchedules.some((schedule) => schedule.enabled && schedule.lastStatus === 'queued')
	);
	let busy = $derived(action !== '');
	let canModify = $derived(allowed('Sys.Modify', nodePath(identity.hostname)));
	let ready = $derived(validatedSchedules && validatedRuns && !schedules.error && !history.error);
	let running = $derived(
		scanActive || activeRun !== null || activeSchedule !== null || requestedAfter !== null
	);
	let startReason = $derived.by(() => {
		if (!canModify) return 'Host modification permission is required.';
		if (!status || statusError) return 'Refresh the pool status before starting a scrub.';
		if (!ready)
			return 'Current scrub history and schedules must be available before starting a scrub.';
		return startBlockReason(status, history.current, displayedSchedules);
	});
	let showStartReason = $derived(
		(status !== null && ready) || Boolean(statusError || schedules.error || history.error)
	);
	let cronDescription = $derived(cronToHuman(draft.cronExpr.trim()));
	let validForm = $derived(
		draft.cronExpr.trim().split(/\s+/).length === 5 &&
			Boolean(cronDescription) &&
			cronDescription !== '-'
	);
	let formLock = $derived.by(() => {
		if (draft.id === null) return '';
		const current = displayedSchedules.find((schedule) => schedule.id === draft.id);
		if (!current) return 'This schedule no longer exists. Close the form and refresh.';
		return scheduleLocked(current)
			? 'Wait for the current scan to finish before editing this schedule.'
			: '';
	});
	let formDisabled = $derived(busy || !canModify || !ready || Boolean(formLock));
	let deleteReason = $derived.by(() => {
		const current = displayedSchedules.find((schedule) => schedule.id === deleteTarget?.id);
		if (!current) return 'This schedule no longer exists. Close this dialog and refresh.';
		return scheduleLocked(current)
			? 'Wait for the current scan to finish before deleting this schedule.'
			: '';
	});

	$effect(() => {
		active = running || queued;
	});
	$effect(() => {
		const baseline = requestedAfter;
		if (
			baseline !== null &&
			(scanActive || history.current.some((run) => run.id > baseline && run.source === 'manual'))
		) {
			requestedAfter = null;
		}
	});

	async function refreshMetadata(fresh = false): Promise<ScrubMetadata | null> {
		revision++;
		priming = false;
		if (fresh && metadataRequest) {
			requests.abort();
			await metadataRequest;
		}
		if (lifetime.signal.aborted) return null;
		if (metadataRequest) return metadataRequest;
		requests = new AbortController();
		const signal = requests.signal;
		metadataLoading = true;
		metadataRequest = (async () => {
			try {
				const [scheduleResult, runResult] = await Promise.all([
					schedules.refetch(),
					history.refetch()
				]);
				if (signal.aborted || lifetime.signal.aborted) return null;
				if (!Array.isArray(scheduleResult) || !Array.isArray(runResult)) return null;
				return {
					schedules: scheduleResult.filter((schedule) => schedule.poolGuid === identity.guid),
					runs: runResult
				};
			} finally {
				metadataRequest = null;
				metadataLoading = false;
			}
		})();
		return metadataRequest;
	}

	async function primeMetadata() {
		const version = revision;
		try {
			const permitted = await canReadHost(identity.hostname);
			lifetime.signal.throwIfAborted();
			if (!permitted || !permissions.data) return;
			const check = currentRequest(lifetime.signal);
			const [cachedSchedules, cachedRuns] = await Promise.all([
				cachedFetch<PoolScrubSchedule[] | null>(
					scrubSchedulesCacheKey,
					async () => null,
					SEVEN_DAYS,
					true,
					identity.hostname,
					PoolScrubScheduleSchema.array().nullable()
				),
				cachedFetch<PoolScrubRun[] | null>(
					scrubRunsCacheKey(identity.guid),
					async () => null,
					SEVEN_DAYS,
					true,
					identity.hostname,
					PoolScrubRunSchema.array()
						.refine((runs) => runs.every((run) => run.poolGuid === identity.guid))
						.nullable()
				)
			]);
			check();
			if (version !== revision) return;
			if (cachedSchedules) schedules.mutate(cachedSchedules);
			if (cachedRuns) history.mutate(cachedRuns);
		} catch (error) {
			if (isRequestCancellation(error)) return;
		} finally {
			if (!lifetime.signal.aborted && version === revision) void refreshMetadata();
		}
	}

	const interval = useInterval(() => (active ? 5_000 : 30_000), {
		callback: () => {
			if (storage.visible && !priming && !busy && !metadataLoading) void refreshMetadata();
		}
	});
	onMount(() => {
		void primeMetadata();
	});
	onDestroy(() => {
		lifetime.abort();
		requests.abort();
		interval.pause();
	});
	watch(
		() => refreshVersion,
		() => {
			if (!busy) void refreshMetadata(true);
		},
		{ lazy: true }
	);
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !priming && !busy) void refreshMetadata();
		},
		{ lazy: true }
	);

	function hasActiveScan(
		sample: ZpoolStatusPool,
		runs: PoolScrubRun[],
		entries: PoolScrubSchedule[]
	) {
		return (
			isPoolScanActive(sample.scan_stats) ||
			runs.some((run) => isPoolScrubActive(run.status)) ||
			entries.some((schedule) => schedule.active || isPoolScrubActive(schedule.lastStatus))
		);
	}
	function startBlockReason(
		sample: ZpoolStatusPool,
		runs: PoolScrubRun[],
		entries: PoolScrubSchedule[]
	) {
		if (requestedAfter !== null || hasActiveScan(sample, runs, entries))
			return 'A scrub or resilver is already starting, running, or paused.';
		if (entries.some((schedule) => schedule.enabled && schedule.lastStatus === 'queued'))
			return 'Disable or delete the queued schedule before starting manually.';
		if (
			['FAULTED', 'UNAVAIL', 'UNAVAILABLE', 'SUSPENDED', 'OFFLINE', 'REMOVED'].includes(
				sample.state.toUpperCase()
			)
		)
			return 'The pool is unavailable for a scrub.';
		return '';
	}

	function scheduleLocked(schedule: PoolScrubSchedule): boolean {
		return schedule.active || running;
	}
	function editableSchedule(snapshot: ScrubSnapshot, scheduleId: number): PoolScrubSchedule {
		const current = snapshot.schedules.find((schedule) => schedule.id === scheduleId);
		if (!current) throw new Error('This schedule no longer exists. Close this dialog and refresh.');
		if (
			requestedAfter !== null ||
			hasActiveScan(snapshot.status, snapshot.runs, snapshot.schedules)
		)
			throw new Error(
				'Wait for the current scrub or resilver to finish before modifying schedules.'
			);
		return current;
	}
	function openSchedule(schedule?: PoolScrubSchedule) {
		if (busy || !canModify || !ready || (schedule && scheduleLocked(schedule))) return;
		draft = schedule
			? { id: schedule.id, cronExpr: schedule.cronExpr, enabled: schedule.enabled }
			: { id: null, cronExpr: '0 3 1 * *', enabled: true };
		formError = '';
		formOpen = true;
	}
	function openConfirmation(kind: 'start' | 'delete', schedule?: PoolScrubSchedule) {
		if (busy || !canModify || !ready) return;
		if (kind === 'start' && startReason) return;
		if (kind === 'delete' && (!schedule || scheduleLocked(schedule))) return;
		deleteTarget = schedule ?? null;
		mutationError = '';
		confirmation = kind;
		confirmOpen = true;
	}
	async function invalidate() {
		revision++;
		requests.abort();
		await metadataRequest;
		await Promise.all([
			removeCache(scrubSchedulesCacheKey, identity.hostname),
			removeCache(scrubRunsCacheKey(identity.guid), identity.hostname),
			removeCache(`zen-pool-status-${identity.guid}`, identity.hostname),
			removeCache(`zfs-dashboard-snapshot:${identity.hostname}`, identity.hostname)
		]);
	}
	async function preflight(): Promise<ScrubSnapshot> {
		const [metadata, sample] = await Promise.all([refreshMetadata(true), refreshStatus()]);
		lifetime.signal.throwIfAborted();
		if (!canModify) throw new Error('Host modification permission is required.');
		if (!metadata || !sample || sample.pool_guid !== identity.guid)
			throw new Error(
				'Current pool status, scrub history, and schedules could not be verified. Refresh and try again.'
			);
		return { ...metadata, status: sample };
	}
	function errorText(error: unknown): string {
		return error instanceof Error ? error.message : 'The scrub action failed. Please try again.';
	}
	async function refreshScrub() {
		if (!lifetime.signal.aborted) await Promise.all([refreshMetadata(true), refreshStatus()]);
	}

	async function startScrub() {
		if (busy || !canModify || !confirmOpen || confirmation !== 'start') return;
		const check = currentRequest(lifetime.signal);
		action = 'start';
		mutationError = '';
		try {
			const snapshot = await preflight();
			check();
			const blocked = startBlockReason(snapshot.status, snapshot.runs, snapshot.schedules);
			if (blocked) throw new Error(blocked);
			const baseline = snapshot.runs.reduce((latest, run) => Math.max(latest, run.id), 0);
			await invalidate();
			check();
			const result = await scrubPool(identity.guid, {
				hostname: identity.hostname,
				signal: lifetime.signal
			});
			await invalidate();
			check();
			if (result.status !== 'success') {
				handleAPIError(result);
				throw new APIRequestError(result);
			}
			requestedAfter = baseline;
			confirmOpen = false;
			tab = 'activity';
			toast.success(`Scrub start requested for ${pool.name}`, { position: 'bottom-center' });
			await refreshScrub();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted)
				mutationError = errorText(error);
		} finally {
			if (!lifetime.signal.aborted) action = '';
		}
	}
	async function saveSchedule(event: SubmitEvent) {
		event.preventDefault();
		if (busy || !canModify || !validForm || formLock) return;
		const check = currentRequest(lifetime.signal);
		const scheduleId = draft.id;
		const input = {
			poolGuid: identity.guid,
			cronExpr: draft.cronExpr.trim(),
			enabled: draft.enabled
		};
		action = 'save';
		formError = '';
		try {
			const snapshot = await preflight();
			check();
			if (scheduleId !== null) editableSchedule(snapshot, scheduleId);
			await invalidate();
			check();
			const options = { hostname: identity.hostname, signal: lifetime.signal };
			const result =
				scheduleId === null
					? await createPoolScrubSchedule(input, options)
					: await updatePoolScrubSchedule(scheduleId, input, options);
			await invalidate();
			check();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				throw new APIRequestError(result);
			}
			if (result.poolGuid !== identity.guid)
				throw new Error('The saved schedule did not match the selected pool.');
			formOpen = false;
			toast.success(scheduleId === null ? 'Scrub schedule created' : 'Scrub schedule updated', {
				position: 'bottom-center'
			});
			await refreshScrub();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) formError = errorText(error);
		} finally {
			if (!lifetime.signal.aborted) action = '';
		}
	}
	async function toggleSchedule(schedule: PoolScrubSchedule) {
		if (busy || !canModify || !ready || scheduleLocked(schedule)) return;
		const check = currentRequest(lifetime.signal);
		const enabled = !schedule.enabled;
		action = 'toggle';
		mutationError = '';
		try {
			const snapshot = await preflight();
			check();
			const current = editableSchedule(snapshot, schedule.id);
			await invalidate();
			check();
			const result = await updatePoolScrubSchedule(
				current.id,
				{ poolGuid: identity.guid, cronExpr: current.cronExpr, enabled },
				{ hostname: identity.hostname, signal: lifetime.signal }
			);
			await invalidate();
			check();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				throw new APIRequestError(result);
			}
			toast.success(enabled ? 'Scrub schedule enabled' : 'Scrub schedule disabled', {
				position: 'bottom-center'
			});
			await refreshScrub();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted)
				mutationError = errorText(error);
		} finally {
			if (!lifetime.signal.aborted) action = '';
		}
	}
	async function removeSchedule() {
		if (busy || !canModify || !confirmOpen || confirmation !== 'delete' || !deleteTarget) return;
		const check = currentRequest(lifetime.signal);
		const scheduleId = deleteTarget.id;
		action = 'delete';
		mutationError = '';
		try {
			const snapshot = await preflight();
			check();
			editableSchedule(snapshot, scheduleId);
			await invalidate();
			check();
			const result = await deletePoolScrubSchedule(scheduleId, {
				hostname: identity.hostname,
				signal: lifetime.signal
			});
			await invalidate();
			check();
			if (result.status !== 'success') {
				handleAPIError(result);
				throw new APIRequestError(result);
			}
			confirmOpen = false;
			deleteTarget = null;
			toast.success('Scrub schedule deleted', { position: 'bottom-center' });
			await refreshScrub();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted)
				mutationError = errorText(error);
		} finally {
			if (!lifetime.signal.aborted) action = '';
		}
	}
</script>

{#snippet controls()}
	<div class="flex flex-wrap items-center gap-2">
		{#if canModify}
			<Button
				variant="default"
				size="sm"
				class="h-8 gap-2"
				disabled={busy || Boolean(startReason)}
				title={startReason || 'Start a scrub'}
				onclick={() => openConfirmation('start')}
			>
				<span class="icon-[lucide--play] size-3.5" aria-hidden="true"></span>Start Scrub
			</Button>
		{/if}
	</div>
{/snippet}

<section class="bg-card flex min-w-0 flex-col rounded-lg border px-4 pt-4 pb-4">
	<header class="flex min-h-8 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 px-2 text-sm">
		<span
			class="bg-zen-highlight text-zen-highlight-foreground flex size-7 shrink-0 items-center justify-center rounded-md"
			aria-hidden="true"
		>
			<span class="icon-[lucide--scan-line] size-5"></span>
		</span>
		<h2 class="text-base leading-6 font-bold">Scan Activity</h2>
		<div class="text-muted-foreground ml-auto text-xs">{@render controls()}</div>
	</header>
	<Tabs.Root bind:value={tab} class="mt-5 min-h-0 min-w-0 flex-1 px-2">
		<Tabs.List class="mb-2 w-full">
			<Tabs.Trigger value="activity" class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm">
				<span class="icon-[lucide--activity] size-3.5" aria-hidden="true"></span>Activity
			</Tabs.Trigger>
			<Tabs.Trigger value="history" class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm">
				<span class="icon-[lucide--history] size-3.5" aria-hidden="true"></span>History
			</Tabs.Trigger>
			<Tabs.Trigger value="schedules" class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm">
				<span class="icon-[lucide--calendar-clock] size-3.5" aria-hidden="true"></span>Schedules
			</Tabs.Trigger>
		</Tabs.List>
		{#if mutationError && !confirmOpen && !formOpen}
			<p class="text-destructive mb-3 text-sm" role="alert">{mutationError}</p>
		{/if}
		<Tabs.Content value="activity" class="mt-0 space-y-3">
			<ScanActivity
				{status}
				{activeRun}
				{activeSchedule}
				{queued}
				requested={requestedAfter !== null}
				loading={statusLoading}
				stale={statusError}
			/>
			{#if history.error || schedules.error}
				<p class="text-muted-foreground text-xs leading-5" role="status">
					Scrub tracking could not be refreshed. Activity may be out of date.
				</p>
			{/if}
			{#if canModify && startReason && showStartReason}
				<p class="text-muted-foreground text-xs leading-5">{startReason}</p>
			{/if}
		</Tabs.Content>
		<Tabs.Content value="history" class="mt-0">
			{#if history.error}
				<p class="text-muted-foreground mb-3 text-xs leading-5" role="status">
					Scrub history could not be refreshed. Showing the last successful history, if available.
				</p>
			{/if}
			<ScrubHistory
				runs={history.current}
				loading={priming || history.loading}
				failed={Boolean(history.error)}
			/>
		</Tabs.Content>
		<Tabs.Content value="schedules" class="mt-0 space-y-4">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<p class="text-muted-foreground max-w-2xl text-xs leading-5">
					Schedules use the node’s local time. Overlapping occurrences and runs within 24 hours of a
					completed scrub are skipped, not postponed. Displayed run times use your browser’s local
					time.
				</p>
				{#if canModify}
					<Button
						variant="outline"
						size="sm"
						class="h-8 shrink-0 gap-1.5"
						disabled={busy || !ready}
						onclick={() => openSchedule()}
					>
						<span class="icon-[lucide--plus] size-3.5" aria-hidden="true"></span>New Schedule
					</Button>
				{/if}
			</div>
			{#if schedules.error}
				<p class="text-muted-foreground text-xs leading-5" role="status">
					Schedules could not be refreshed. Cached schedules are shown; modification is disabled
					until a fresh read succeeds.
				</p>
			{/if}
			{#each displayedSchedules as schedule (schedule.id)}
				<section class="rounded-md border p-3 sm:p-4" aria-label="Scrub schedule {schedule.id}">
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0 space-y-1.5">
							<h3 class="text-[13px] font-semibold">
								{cronToHuman(schedule.cronExpr) || 'Custom schedule'}
							</h3>
							<div class="flex flex-wrap items-center gap-2">
								<Badge
									variant="outline"
									class="border-muted-foreground/30 bg-muted gap-1.5 px-2.5 py-1 font-semibold"
								>
									<span
										class="text-muted-foreground size-3.5 shrink-0 {schedule.enabled
											? 'icon-[lucide--check]'
											: 'icon-[lucide--pause]'}"
										aria-hidden="true"
									></span>
									{schedule.enabled ? 'Enabled' : 'Disabled'}
								</Badge>
								<Badge
									variant="outline"
									class="border-muted-foreground/30 bg-muted gap-1.5 px-2.5 py-1 font-semibold"
								>
									<span
										class="size-1.5 shrink-0 rounded-full {scrubDot[
											scrubTone(schedule.lastStatus)
										]}"
										aria-hidden="true"
									></span>{scrubLabel(schedule.lastStatus)}
								</Badge>
							</div>
						</div>
						{#if canModify}
							<div class="flex shrink-0 items-center gap-1">
								<Button
									variant="ghost"
									size="icon"
									class="mr-1 h-8 w-11"
									role="switch"
									aria-checked={schedule.enabled}
									aria-label="Scrub schedule {schedule.id} enabled"
									title={schedule.enabled ? 'Disable schedule' : 'Enable schedule'}
									disabled={busy || !ready || scheduleLocked(schedule)}
									onclick={() => toggleSchedule(schedule)}
								>
									<span
										class="inline-flex h-5 w-9 shrink-0 items-center rounded-full border p-0.5 transition-colors {schedule.enabled
											? 'border-zen-accent bg-zen-accent'
											: 'border-muted-foreground/40 bg-muted'}"
										aria-hidden="true"
									>
										<span
											class="block size-3.5 shrink-0 rounded-full bg-white shadow-sm transition-transform motion-reduce:transition-none {schedule.enabled
												? 'translate-x-4'
												: 'translate-x-0'}"
										></span>
									</span>
								</Button>
								<Button
									variant="ghost"
									size="icon"
									class="size-8"
									disabled={busy || !ready || scheduleLocked(schedule)}
									aria-label="Edit scrub schedule {schedule.id}"
									title="Edit schedule"
									onclick={() => openSchedule(schedule)}
									><span class="icon-[lucide--pencil] size-3.5" aria-hidden="true"></span></Button
								>
								<Button
									variant="ghost"
									size="icon"
									class="text-destructive size-8"
									disabled={busy || !ready || scheduleLocked(schedule)}
									aria-label="Delete scrub schedule {schedule.id}"
									title="Delete schedule"
									onclick={() => openConfirmation('delete', schedule)}
									><span class="icon-[lucide--trash-2] size-3.5" aria-hidden="true"></span></Button
								>
							</div>
						{/if}
					</div>
					<dl class="mt-4 grid gap-3 text-[13px] sm:grid-cols-2">
						<div>
							<dt class="text-muted-foreground text-xs">Next Run</dt>
							<dd class="mt-1 tabular-nums">
								{schedule.enabled ? scanDate(schedule.nextRunAt, 'Not Scheduled') : 'Disabled'}
							</dd>
						</div>
						<div>
							<dt class="text-muted-foreground text-xs">Last Run</dt>
							<dd class="mt-1 tabular-nums">{scanDate(schedule.lastRunAt, 'Never')}</dd>
						</div>
					</dl>
					{#if schedule.enabled && schedule.lastStatus === 'queued'}
						<p class="text-muted-foreground mt-3 text-xs leading-5">
							Queued {scanDate(schedule.queuedAt, '(time not reported)')}. Disable or delete this
							schedule to cancel its pending occurrence.
						</p>
					{/if}
					{#if schedule.lastError}
						<p class="text-muted-foreground mt-3 text-xs leading-5 break-words">
							{schedule.lastError}
						</p>
					{/if}
					{#if scheduleLocked(schedule)}
						<p class="text-muted-foreground mt-3 text-xs leading-5">
							Existing schedules cannot be modified while a scrub or resilver is active.
						</p>
					{/if}
				</section>
			{:else}
				<p
					class="text-muted-foreground border-muted-foreground/40 flex items-center justify-center gap-2.5 rounded-md border-2 border-dashed px-4 py-6 text-center text-sm"
					role="status"
				>
					<span
						class="size-5 shrink-0 {priming || schedules.loading
							? 'icon-[lucide--loader-circle] motion-safe:animate-spin'
							: schedules.error
								? 'icon-[lucide--circle-alert]'
								: 'icon-[lucide--calendar-clock]'}"
						aria-hidden="true"
					></span>
					<span class="min-w-0">
						{priming || schedules.loading
							? 'Loading scrub schedules…'
							: schedules.error
								? 'Scrub schedules are unavailable.'
								: 'No scrub schedules are configured for this pool.'}
					</span>
				</p>
			{/each}
		</Tabs.Content>
	</Tabs.Root>
</section>

<Dialog.Root bind:open={formOpen}>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		showCloseButton={false}
		escapeKeydownBehavior={busy ? 'ignore' : 'close'}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={saveSchedule}>
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="icon-[lucide--calendar-clock] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>{draft.id === null ? 'New Scrub Schedule' : 'Edit Scrub Schedule'}
					</Dialog.Title>
					<Dialog.Description class="text-xs break-words">
						Schedule integrity checks for {pool.name} using the node’s local time.
					</Dialog.Description>
				</Dialog.Header>
				<Dialog.Close disabled={busy}>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0" disabled={busy}>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</Dialog.Close>
			</div>
			<div class="min-h-0 space-y-4 overflow-y-auto p-4">
				<div class="space-y-2">
					<Label for="{id}-cron">Schedule</Label>
					<Input
						id="{id}-cron"
						class="font-mono"
						bind:value={draft.cronExpr}
						disabled={formDisabled}
						required
						placeholder="0 3 1 * *"
						aria-describedby="{id}-cron-help"
						aria-invalid={!validForm}
					/>
					<div class="flex flex-wrap gap-2">
						<Button
							type="button"
							variant="outline"
							size="sm"
							class="h-7 text-xs"
							disabled={formDisabled}
							onclick={() => (draft.cronExpr = '0 3 * * 0')}>Weekly · Sunday 03:00</Button
						>
						<Button
							type="button"
							variant="outline"
							size="sm"
							class="h-7 text-xs"
							disabled={formDisabled}
							onclick={() => (draft.cronExpr = '0 3 1 * *')}>Monthly · Day 1, 03:00</Button
						>
					</div>
					<p id="{id}-cron-help" class="text-muted-foreground text-xs leading-5">
						{validForm
							? cronDescription
							: 'Enter a valid five-field cron expression: minute, hour, day, month, weekday.'}
					</p>
				</div>
				{#if formLock}
					<p class="text-muted-foreground text-sm" role="status">{formLock}</p>
				{/if}
				{#if formError}
					<p class="text-destructive text-sm break-words" role="alert">{formError}</p>
				{/if}
			</div>
			<div class="flex shrink-0 justify-end border-t p-4">
				<Button type="submit" class="h-9 gap-2" disabled={formDisabled || !validForm}>
					{#if action === 'save'}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}Save Schedule
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root
	open={confirmOpen}
	onOpenChange={(open) => {
		if (!busy) confirmOpen = open;
	}}
>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={busy ? 'ignore' : 'close'}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="size-5 shrink-0 {confirmation === 'start'
							? 'icon-[lucide--scan-line] text-zen-highlight-foreground'
							: 'icon-[lucide--trash-2] text-destructive'}"
						aria-hidden="true"
					></span>{confirmation === 'start' ? 'Start Scrub?' : 'Delete Scrub Schedule?'}
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={busy}>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0" disabled={busy}>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 space-y-3 overflow-y-auto p-4">
			<AlertDialog.Description
				class="w-full text-left leading-relaxed whitespace-normal text-pretty [overflow-wrap:anywhere]"
			>
				{confirmation === 'start'
					? `A scrub will verify the stored data in ${pool.name} and repair damage where redundancy permits. The pool stays online, but I/O performance may be reduced for hours or days. Closing this page will not stop the scrub.`
					: `Schedule #${deleteTarget?.id} for ${pool.name} and its queued occurrence will be removed. Other schedules and completed history will be retained.`}
			</AlertDialog.Description>
			{#if confirmation === 'start' ? startReason : deleteReason}
				<p class="text-muted-foreground text-sm" role="status">
					{confirmation === 'start' ? startReason : deleteReason}
				</p>
			{/if}
			{#if mutationError}
				<p class="text-destructive text-sm break-words" role="alert">{mutationError}</p>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button
				variant={confirmation === 'delete' ? 'destructive' : 'default'}
				class="gap-2"
				disabled={busy ||
					!canModify ||
					!ready ||
					Boolean(confirmation === 'start' ? startReason : deleteReason)}
				onclick={() => (confirmation === 'start' ? startScrub() : removeSchedule())}
			>
				{#if busy}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}{confirmation === 'start' ? 'Start Scrub' : 'Delete Schedule'}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
