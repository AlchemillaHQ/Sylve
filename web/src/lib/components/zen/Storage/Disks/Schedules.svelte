<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		createSmartSelfTestSchedule,
		deleteSmartSelfTestSchedule,
		updateSmartSelfTestSchedule
	} from '$lib/api/disk/disk';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type {
		Disk,
		SmartSelfTestCapabilities,
		SmartSelfTestSchedule,
		SmartSelfTestScheduleType
	} from '$lib/types/disk/disk';
	import { cn } from '$lib/utils.js';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { scanDate, scrubDot } from '../Pools/scan';
	import { diskActionError } from './disks';
	import {
		durationMinutes,
		formatDuration,
		schedulableKinds,
		scheduleActive,
		schedulePresets,
		scheduleStatusLabel,
		scheduleTone,
		testLabel
	} from './selftest';

	let {
		disk,
		hostname,
		canModify,
		capabilities,
		schedules,
		stale,
		onSaved,
		onDeleted
	}: {
		disk: Disk;
		hostname: string;
		canModify: boolean;
		capabilities: SmartSelfTestCapabilities;
		schedules: SmartSelfTestSchedule[];
		stale: boolean;
		onSaved: (schedule: SmartSelfTestSchedule) => void;
		onDeleted: (id: number) => void;
	} = $props();

	const id = $props.id();
	let destroyed = false;
	onDestroy(() => (destroyed = true));

	let formOpen = $state(false);
	let draft = $state({
		id: null as number | null,
		testType: 'short' as SmartSelfTestScheduleType,
		cronExpr: '',
		enabled: true
	});
	let saving = $state(false);
	let formError = $state('');
	let toggling = $state<number | null>(null);
	let deleteTarget = $state<SmartSelfTestSchedule | null>(null);
	let deleteOpen = $state(false);
	let deleting = $state(false);
	let deleteError = $state('');
	let busy = $derived(saving || deleting || toggling !== null);

	let supported = $derived(schedulableKinds.filter((kind) => capabilities[kind]));
	let addable = $derived(
		supported.filter((kind) => !schedules.some((schedule) => schedule.testType === kind))
	);
	let addBlocked = $derived.by(() => {
		if (!disk.identityStable) {
			return 'Schedules need a drive with a unique serial number, so they follow it if its device name changes.';
		}
		if (supported.length === 0)
			return 'This drive does not support scheduled Short or Extended tests.';
		if (addable.length === 0) return 'Short and Extended tests both have a schedule already.';
		if (stale) return 'Schedules could not be refreshed. Try again once they load.';
		return '';
	});
	let cronDescription = $derived(cronToHuman(draft.cronExpr.trim()));
	let validCron = $derived(
		draft.cronExpr.trim().split(/\s+/).length === 5 &&
			Boolean(cronDescription) &&
			cronDescription !== '-'
	);

	function openForm(schedule?: SmartSelfTestSchedule) {
		if (busy || !canModify) return;
		if (schedule) {
			if (scheduleActive(schedule)) return;
			draft = {
				id: schedule.id,
				testType: schedule.testType,
				cronExpr: schedule.cronExpr,
				enabled: schedule.enabled
			};
		} else {
			if (addBlocked) return;
			const testType = addable[0];
			draft = {
				id: null,
				testType,
				cronExpr: schedulePresets[testType][0].cronExpr,
				enabled: true
			};
		}
		formError = '';
		formOpen = true;
	}

	function chooseType(value: string) {
		const testType = value as SmartSelfTestScheduleType;
		const preset = schedulePresets[draft.testType].some((item) => item.cronExpr === draft.cronExpr);
		draft.testType = testType;
		if (preset || !draft.cronExpr.trim()) draft.cronExpr = schedulePresets[testType][0].cronExpr;
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		if (busy || !validCron || !canModify) return;
		const editing = draft.id;
		const input = {
			device: disk.device,
			testType: draft.testType,
			cronExpr: draft.cronExpr.trim(),
			enabled: draft.enabled
		};
		saving = true;
		formError = '';
		try {
			const response = editing
				? await updateSmartSelfTestSchedule(editing, input, { hostname })
				: await createSmartSelfTestSchedule(input, { hostname });
			if (destroyed) return;
			if (isAPIResponse(response)) {
				formError = diskActionError(response, 'Could not save the schedule. Please try again.');
				handleAPIError(response);
				return;
			}
			onSaved(response);
			formOpen = false;
			toast.success(editing ? 'Self-test schedule updated' : 'Self-test schedule created', {
				position: 'bottom-center'
			});
		} catch (error) {
			if (!isRequestCancellation(error) && !destroyed)
				formError = 'Could not save the schedule. Please try again.';
		} finally {
			if (!destroyed) saving = false;
		}
	}

	async function toggle(schedule: SmartSelfTestSchedule) {
		if (busy || !canModify || scheduleActive(schedule)) return;
		const enabled = !schedule.enabled;
		toggling = schedule.id;
		try {
			const response = await updateSmartSelfTestSchedule(
				schedule.id,
				{ device: disk.device, testType: schedule.testType, cronExpr: schedule.cronExpr, enabled },
				{ hostname }
			);
			if (destroyed) return;
			if (isAPIResponse(response)) {
				toast.error(diskActionError(response, 'Could not change the schedule. Please try again.'), {
					position: 'bottom-center'
				});
				handleAPIError(response);
				return;
			}
			onSaved(response);
			toast.success(enabled ? 'Self-test schedule enabled' : 'Self-test schedule disabled', {
				position: 'bottom-center'
			});
		} catch (error) {
			if (!isRequestCancellation(error) && !destroyed) {
				toast.error('Could not change the schedule. Please try again.', {
					position: 'bottom-center'
				});
			}
		} finally {
			if (!destroyed) toggling = null;
		}
	}

	function confirmDelete(schedule: SmartSelfTestSchedule) {
		if (busy || !canModify || scheduleActive(schedule)) return;
		deleteTarget = schedule;
		deleteError = '';
		deleteOpen = true;
	}

	async function remove() {
		const target = deleteTarget;
		if (busy || !target || !canModify) return;
		deleting = true;
		deleteError = '';
		try {
			const response = await deleteSmartSelfTestSchedule(target.id, { hostname });
			if (destroyed) return;
			if (response.status !== 'success') {
				deleteError = diskActionError(response, 'Could not delete the schedule. Please try again.');
				handleAPIError(response);
				return;
			}
			onDeleted(target.id);
			deleteOpen = false;
			toast.success('Self-test schedule deleted', { position: 'bottom-center' });
		} catch (error) {
			if (!isRequestCancellation(error) && !destroyed)
				deleteError = 'Could not delete the schedule. Please try again.';
		} finally {
			if (!destroyed) deleting = false;
		}
	}
</script>

{#snippet newButton()}
	{#if addBlocked}
		<Tooltip.Root delayDuration={500}>
			<Tooltip.Trigger
				type="button"
				aria-disabled="true"
				class={cn(
					buttonVariants({ variant: 'outline', size: 'sm' }),
					'h-8 shrink-0 cursor-help gap-1.5 aria-disabled:pointer-events-auto'
				)}
			>
				<span class="icon-[lucide--plus] size-3.5" aria-hidden="true"></span>New Schedule
				<span class="sr-only">: {addBlocked}</span>
			</Tooltip.Trigger>
			<Tooltip.Content
				side="bottom"
				align="end"
				sideOffset={6}
				class="z-60 max-w-64 text-left text-pretty"
			>
				{addBlocked}
			</Tooltip.Content>
		</Tooltip.Root>
	{:else}
		<Button
			variant="outline"
			size="sm"
			class="h-8 shrink-0 gap-1.5"
			disabled={busy}
			onclick={() => openForm()}
		>
			<span class="icon-[lucide--plus] size-3.5" aria-hidden="true"></span>New Schedule
		</Button>
	{/if}
{/snippet}

<section class="space-y-3" aria-labelledby="{id}-schedules">
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div class="min-w-0 space-y-1">
			<div class="flex items-center gap-1.5">
				<h3 id="{id}-schedules" class="text-sm font-medium">Schedules</h3>
				<Tooltip.Root delayDuration={150}>
					<Tooltip.Trigger
						type="button"
						aria-label="Help for Schedules"
						class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
					>
						<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
					</Tooltip.Trigger>
					<Tooltip.Content
						side="top"
						align="start"
						sideOffset={6}
						class="z-60 max-w-64 text-left leading-5"
					>
						A drive can have one Short and one Extended schedule. A scheduled test waits its turn if
						another disk test is running.
					</Tooltip.Content>
				</Tooltip.Root>
			</div>
			<p class="text-muted-foreground text-xs leading-5">
				Schedules run in the node’s time zone; times below are in yours.
			</p>
		</div>
		{#if canModify}{@render newButton()}{/if}
	</div>
	{#if stale}
		<p class="text-muted-foreground text-xs leading-5" role="status">
			Schedules could not be refreshed. The last loaded schedules are shown, and changes are
			disabled until they load.
		</p>
	{/if}
	{#each schedules as schedule (schedule.id)}
		{@const locked = scheduleActive(schedule)}
		{@const description = cronToHuman(schedule.cronExpr)}
		<section
			class="rounded-md border p-3 sm:p-4"
			aria-label="{testLabel(schedule.testType)} test schedule"
		>
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="min-w-0 space-y-1.5">
					<h4 class="text-[13px] font-semibold">{testLabel(schedule.testType)} Test</h4>
					<p class="text-muted-foreground text-xs" title={schedule.cronExpr}>
						{description || schedule.cronExpr}
					</p>
					<div class="flex flex-wrap items-center gap-2 pt-0.5">
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
								class="size-1.5 shrink-0 rounded-full {scrubDot[scheduleTone(schedule.lastStatus)]}"
								aria-hidden="true"
							></span>{scheduleStatusLabel(schedule)}
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
							aria-label="{testLabel(schedule.testType)} test schedule enabled"
							title={schedule.enabled ? 'Disable schedule' : 'Enable schedule'}
							disabled={busy || stale || locked}
							onclick={() => toggle(schedule)}
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
							disabled={busy || stale || locked}
							aria-label="Edit {testLabel(schedule.testType)} test schedule"
							title="Edit schedule"
							onclick={() => openForm(schedule)}
							><span class="icon-[lucide--pencil] size-3.5" aria-hidden="true"></span></Button
						>
						<Button
							variant="ghost"
							size="icon"
							class="text-destructive size-8"
							disabled={busy || stale || locked}
							aria-label="Delete {testLabel(schedule.testType)} test schedule"
							title="Delete schedule"
							onclick={() => confirmDelete(schedule)}
							><span class="icon-[lucide--trash-2] size-3.5" aria-hidden="true"></span></Button
						>
					</div>
				{/if}
			</div>
			<dl class="mt-4 grid gap-3 text-[13px] sm:grid-cols-3">
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
				<div>
					<dt class="text-muted-foreground text-xs">Duration</dt>
					<dd class="mt-1">
						{formatDuration(
							schedule.estimatedMinutes || durationMinutes(schedule.testType, capabilities)
						)}
					</dd>
				</div>
			</dl>
			{#if schedule.enabled && schedule.lastStatus === 'queued'}
				<p class="text-muted-foreground mt-3 text-xs leading-5">
					Queued {scanDate(schedule.queuedAt, '(time not reported)')}. It starts when no other disk
					test is running.
				</p>
			{/if}
			{#if schedule.lastError}
				<p class="text-muted-foreground mt-3 text-xs leading-5 break-words">{schedule.lastError}</p>
			{/if}
			{#if locked && canModify}
				<p class="text-muted-foreground mt-3 text-xs leading-5">
					This schedule cannot be changed while its test is running.
				</p>
			{/if}
		</section>
	{:else}
		<p
			class="text-muted-foreground border-muted-foreground/40 flex items-center justify-center gap-2.5 rounded-md border-2 border-dashed px-4 py-6 text-center text-sm"
			role="status"
		>
			<span class="icon-[lucide--calendar-clock] size-5 shrink-0" aria-hidden="true"></span>
			<span class="min-w-0">No self-test schedules are configured for this drive.</span>
		</p>
	{/each}
</section>

<Dialog.Root
	open={formOpen}
	onOpenChange={(open) => {
		if (!saving) formOpen = open;
	}}
>
	<Dialog.Content
		class="z-60 flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		overlayClass="z-60"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save}>
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="icon-[lucide--calendar-clock] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>{draft.id === null ? 'New Self-Test Schedule' : 'Edit Self-Test Schedule'}
					</Dialog.Title>
					<Dialog.Description class="text-xs break-words">
						Run tests on {disk.device} using the node’s local time.
					</Dialog.Description>
				</Dialog.Header>
				<Dialog.Close disabled={saving}>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost"
							size="icon"
							class="size-9 shrink-0"
							disabled={saving}
						>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</Dialog.Close>
			</div>
			<div class="min-h-0 space-y-4 overflow-y-auto p-4">
				<div class="space-y-2">
					<Label for="{id}-type">Test Type</Label>
					<Select.Root
						type="single"
						value={draft.testType}
						onValueChange={chooseType}
						disabled={saving || draft.id !== null}
					>
						<Select.Trigger id="{id}-type" class="bg-background w-full dark:hover:bg-input/30">
							{testLabel(draft.testType)}
						</Select.Trigger>
						<Select.Content class="z-60">
							{#each draft.id === null ? addable : [draft.testType] as kind (kind)}
								<Select.Item value={kind} label={testLabel(kind)} />
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				<div class="space-y-2">
					<Label for="{id}-cron">Schedule</Label>
					<Input
						id="{id}-cron"
						class="font-mono"
						bind:value={draft.cronExpr}
						disabled={saving}
						required
						placeholder="0 3 * * 0"
						autocomplete="off"
						spellcheck={false}
						aria-describedby="{id}-cron-help"
						aria-invalid={!validCron}
					/>
					<div class="flex flex-wrap gap-2">
						{#each schedulePresets[draft.testType] as preset (preset.cronExpr)}
							<Button
								type="button"
								variant="outline"
								size="sm"
								class="h-7 text-xs"
								disabled={saving}
								onclick={() => (draft.cronExpr = preset.cronExpr)}>{preset.label}</Button
							>
						{/each}
					</div>
					<p id="{id}-cron-help" class="text-muted-foreground text-xs leading-5">
						{validCron
							? cronDescription
							: 'Enter a valid five-field cron expression: minute, hour, day, month, weekday.'}
					</p>
				</div>
				{#if formError}<p class="text-destructive text-sm break-words" role="alert">
						{formError}
					</p>{/if}
			</div>
			<div class="flex shrink-0 justify-end border-t p-4">
				<Button type="submit" class="h-9 gap-2" disabled={saving || !validCron || !canModify}>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}Save Schedule
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root
	open={deleteOpen}
	onOpenChange={(open) => {
		if (!deleting) deleteOpen = open;
	}}
>
	<AlertDialog.Content
		class="z-60 flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		overlayClass="z-60"
		escapeKeydownBehavior={deleting ? 'ignore' : 'close'}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					Delete Schedule?
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
		<div class="min-h-0 space-y-3 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				This deletes the {deleteTarget ? testLabel(deleteTarget.testType) : ''} test schedule for
				<span class="text-foreground text-sm font-semibold break-all">{disk.device}</span>
				on <span class="text-foreground text-sm font-semibold break-all">{hostname}</span>,
				including a run waiting in the queue. Results of tests it already ran stay in the drive’s
				log.
			</AlertDialog.Description>
			{#if deleteError}<p class="text-destructive text-sm break-words" role="alert">
					{deleteError}
				</p>{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button
				variant="destructive"
				class="gap-2"
				disabled={deleting || !canModify}
				onclick={remove}
			>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}Delete Schedule
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
