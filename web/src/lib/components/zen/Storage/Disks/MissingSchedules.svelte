<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		deleteSmartSelfTestSchedule,
		listSmartSelfTestSchedules,
		updateSmartSelfTestSchedule
	} from '$lib/api/disk/disk';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Disk, SmartSelfTestSchedule } from '$lib/types/disk/disk';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { diskActionError } from './disks';
	import { scheduleActive, testLabel } from './selftest';

	let {
		disks,
		ready,
		hostname,
		canModify
	}: {
		disks: Disk[];
		ready: boolean;
		hostname: string;
		canModify: boolean;
	} = $props();

	const lifetime = new AbortController();
	onDestroy(() => lifetime.abort());
	let schedules = $state<SmartSelfTestSchedule[]>([]);
	let open = $state(false);
	let working = $state<number | null>(null);
	let deleteTarget = $state<SmartSelfTestSchedule | null>(null);
	let deleteOpen = $state(false);
	let deleteError = $state('');

	let present = $derived(new Set(disks.map((disk) => disk.uuid)));
	let missing = $derived(
		ready ? schedules.filter((schedule) => !present.has(schedule.diskKey)) : []
	);
	let inventory = $derived([...present].sort().join('\n'));

	async function load() {
		try {
			const list = await listSmartSelfTestSchedules({ hostname, signal: lifetime.signal });
			if (!lifetime.signal.aborted && Array.isArray(list)) schedules = list;
		} catch (error) {
			if (!isRequestCancellation(error)) console.error('Could not read self-test schedules', error);
		}
	}

	$effect(() => {
		if (!ready) return;
		void inventory;
		void load();
	});

	$effect(() => {
		if (missing.length === 0 && !deleteOpen) open = false;
	});

	async function disable(schedule: SmartSelfTestSchedule) {
		if (working !== null || !canModify || !schedule.enabled || scheduleActive(schedule)) return;
		working = schedule.id;
		try {
			const response = await updateSmartSelfTestSchedule(
				schedule.id,
				{
					device: schedule.device,
					testType: schedule.testType,
					cronExpr: schedule.cronExpr,
					enabled: false
				},
				{ hostname }
			);
			if (lifetime.signal.aborted) return;
			if (isAPIResponse(response)) {
				toast.error(
					diskActionError(response, 'Could not disable the schedule. Please try again.'),
					{
						position: 'bottom-center'
					}
				);
				handleAPIError(response);
				return;
			}
			schedules = schedules.map((item) => (item.id === response.id ? response : item));
			toast.success(`Schedule for ${schedule.device} disabled`, { position: 'bottom-center' });
		} finally {
			if (!lifetime.signal.aborted) working = null;
		}
	}

	async function remove() {
		const target = deleteTarget;
		if (working !== null || !target || !canModify) return;
		working = target.id;
		deleteError = '';
		try {
			const response = await deleteSmartSelfTestSchedule(target.id, { hostname });
			if (lifetime.signal.aborted) return;
			if (response.status !== 'success') {
				deleteError = diskActionError(response, 'Could not delete the schedule. Please try again.');
				handleAPIError(response);
				return;
			}
			schedules = schedules.filter((item) => item.id !== target.id);
			deleteOpen = false;
			toast.success(`Schedule for ${target.device} deleted`, { position: 'bottom-center' });
		} finally {
			if (!lifetime.signal.aborted) working = null;
		}
	}
</script>

{#if missing.length > 0}
	<div
		class="bg-card mb-4 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
		role="status"
	>
		<span
			class="icon-[lucide--calendar-x] size-4 shrink-0 text-amber-600 dark:text-amber-400"
			aria-hidden="true"
		></span>
		<p class="min-w-0 flex-1">
			{missing.length === 1
				? '1 self-test schedule is'
				: `${missing.length} self-test schedules are`} for drives that are not connected.
		</p>
		<Button variant="outline" size="sm" onclick={() => (open = true)}>Review</Button>
	</div>
{/if}

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
		showCloseButton={false}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 gap-1 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="icon-[lucide--calendar-x] size-5 shrink-0 text-amber-600 dark:text-amber-400"
						aria-hidden="true"
					></span>Schedules for Missing Drives
				</Dialog.Title>
				<Dialog.Description class="text-xs break-words">
					These drives are not connected to {hostname}. Their schedules can only be disabled or
					deleted.
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Close>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0">
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</Dialog.Close>
		</div>
		<ul class="min-h-0 space-y-3 overflow-y-auto p-4">
			{#each missing as schedule (schedule.id)}
				<li class="rounded-md border p-3">
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0 space-y-1">
							<p class="flex flex-wrap items-baseline gap-x-2 text-sm">
								<span class="font-mono font-medium">{schedule.device}</span>
								<span class="text-muted-foreground text-xs break-words">
									{[schedule.model, schedule.serial].filter(Boolean).join(' · ')}
								</span>
							</p>
							<p class="text-muted-foreground text-xs" title={schedule.cronExpr}>
								{testLabel(schedule.testType)} test · {cronToHuman(schedule.cronExpr) ||
									schedule.cronExpr}
							</p>
							<Badge
								variant="outline"
								class="border-muted-foreground/30 bg-muted mt-1 gap-1.5 px-2.5 py-1 font-semibold"
							>
								<span
									class="text-muted-foreground size-3.5 shrink-0 {schedule.enabled
										? 'icon-[lucide--check]'
										: 'icon-[lucide--pause]'}"
									aria-hidden="true"
								></span>
								{schedule.enabled ? 'Enabled' : 'Disabled'}
							</Badge>
						</div>
						{#if canModify}
							<div class="flex shrink-0 items-center gap-2">
								<Button
									variant="outline"
									size="sm"
									class="h-8 gap-2"
									disabled={working !== null || !schedule.enabled || scheduleActive(schedule)}
									onclick={() => disable(schedule)}
								>
									{#if working === schedule.id && !deleteOpen}
										<span
											class="icon-[lucide--loader-circle] size-3.5 animate-spin"
											aria-hidden="true"
										></span>
									{/if}
									{schedule.enabled ? 'Disable' : 'Disabled'}
								</Button>
								<Button
									variant="ghost"
									size="icon"
									class="text-destructive size-8"
									disabled={working !== null || scheduleActive(schedule)}
									aria-label="Delete the schedule for {schedule.device}"
									title="Delete schedule"
									onclick={() => {
										deleteTarget = schedule;
										deleteError = '';
										deleteOpen = true;
									}}
									><span class="icon-[lucide--trash-2] size-3.5" aria-hidden="true"></span></Button
								>
							</div>
						{/if}
					</div>
				</li>
			{/each}
		</ul>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root
	open={deleteOpen}
	onOpenChange={(value) => {
		if (working === null) deleteOpen = value;
	}}
>
	<AlertDialog.Content
		class="z-60 flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		overlayClass="z-60"
		escapeKeydownBehavior={working !== null ? 'ignore' : 'close'}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					Delete Schedule?
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={working !== null}>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={working !== null}
					>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 space-y-3 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				This deletes the {deleteTarget ? testLabel(deleteTarget.testType) : ''} test schedule for the
				missing drive
				<span class="text-foreground text-sm font-semibold break-all">{deleteTarget?.device}</span>.
				If the drive comes back, it will need a new schedule.
			</AlertDialog.Description>
			{#if deleteError}<p class="text-destructive text-sm break-words" role="alert">
					{deleteError}
				</p>{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button
				variant="destructive"
				class="gap-2"
				disabled={working !== null || !canModify}
				onclick={remove}
			>
				{#if working !== null}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}Delete Schedule
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
