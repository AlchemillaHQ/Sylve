<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { createSnapshotSchedule, editSnapshotSchedule } from '$lib/api/zfs/datasets';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { DetailedDataset, PeriodicSnapshot } from '$lib/types/zfs/dataset';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { cronToHuman } from '$lib/utils/time';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import DatasetPicker from '../FileSystems/DatasetPicker.svelte';
	import { isVolume } from '../FileSystems/filesystems';
	import {
		CRON,
		countProblem,
		cronProblem,
		editScheduleDraft,
		frequencyChoices,
		gfsFields,
		newScheduleDraft,
		prefixProblem,
		retentionChoices,
		retentionProblem,
		scheduleSaveErrorMessage,
		scheduleSettings,
		settingsChanged,
		simpleFields,
		type CountKey
	} from './schedule';

	let {
		hostname,
		job,
		datasetName = '',
		choices,
		choicesFailed = false,
		onRetryChoices,
		jobs,
		onClose,
		onDone
	}: {
		hostname: string;
		job?: PeriodicSnapshot;
		datasetName?: string;
		choices: DetailedDataset[] | null;
		choicesFailed?: boolean;
		onRetryChoices?: () => void;
		jobs: PeriodicSnapshot[];
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const editing = untrack(() => job);
	const editingName = untrack(() => datasetName);
	const initialSettings = scheduleSettings(
		editing ? editScheduleDraft(editing, editingName) : newScheduleDraft()
	);

	let draft = $state(editing ? editScheduleDraft(editing, editingName) : newScheduleDraft());
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');

	let frequencies = $derived(frequencyChoices(draft.frequency));
	let datasetProblem = $derived(editing || draft.dataset ? '' : 'Choose a file system or volume');
	let canRecurse = $derived(!isVolume(draft.dataset));
	let prefixError = $derived(editing ? '' : prefixProblem(draft.prefix, draft.dataset, jobs));
	let cronError = $derived(draft.frequency === CRON ? cronProblem(draft.cron) : '');
	let cronDescription = $derived(
		draft.frequency === CRON && !cronError ? cronToHuman(draft.cron.trim()) : ''
	);
	let fields = $derived(draft.retention === 'gfs' ? gfsFields : simpleFields);
	let countErrors = $derived(
		draft.retention === 'all'
			? []
			: fields.map((field) => countProblem(fieldValue(field.key))).filter(Boolean)
	);
	let retentionError = $derived(retentionProblem(draft));
	let valid = $derived(
		!datasetProblem && !prefixError && !cronError && countErrors.length === 0 && !retentionError
	);
	let settings = $derived(scheduleSettings(draft));
	let dirty = $derived(!editing || settingsChanged(initialSettings, settings));

	onDestroy(() => lifetime.abort());

	function fieldValue(key: CountKey): string {
		if (draft.retention === 'gfs') return key === 'maxAgeDays' ? '' : draft.gfs[key];
		return key === 'keepLast' || key === 'maxAgeDays' ? draft.simple[key] : '';
	}

	function setFieldValue(key: CountKey, value: string) {
		if (draft.retention === 'gfs') {
			if (key !== 'maxAgeDays') draft.gfs[key] = value;
		} else if (key === 'keepLast' || key === 'maxAgeDays') {
			draft.simple[key] = value;
		}
	}

	function shown(key: string, problem: string, value: string): string {
		return attempted || (touched[key] && value.trim()) ? problem : '';
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (saving || !valid || !dirty) return;
		saving = true;
		saveError = '';
		const options = { hostname, signal: lifetime.signal };
		try {
			const response = editing
				? await editSnapshotSchedule(editing.id, settings, options)
				: await createSnapshotSchedule(
						draft.dataset!,
						draft.prefix.trim(),
						canRecurse && draft.recursive,
						settings,
						options
					);
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(
				editing
					? 'Snapshot schedule saved'
					: `Snapshot schedule created for ${draft.dataset!.name}`,
				{ position: 'bottom-center' }
			);
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = scheduleSaveErrorMessage(
				error instanceof Error ? error.message : 'The schedule could not be saved.'
			);
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

{#snippet hint(text: string, description: string)}
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {text}"
			class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
		>
			<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
		</Tooltip.Trigger>
		<Tooltip.Content
			side="top"
			align="end"
			sideOffset={6}
			class="z-60 max-w-72 text-left leading-5"
		>
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet fieldLabel(key: string, text: string, error: string, description?: string)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<Label for="{id}-{key}">{text}</Label>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{#if description}{@render hint(text, description)}{/if}
	</div>
{/snippet}

{#snippet countField(key: CountKey, text: string, description?: string)}
	{@const value = fieldValue(key)}
	{@const error = shown(`${draft.retention}-${key}`, countProblem(value), value)}
	<div class="min-w-0 space-y-2">
		{@render fieldLabel(`${draft.retention}-${key}`, text, error, description)}
		<Input
			id="{id}-{draft.retention}-{key}"
			{value}
			oninput={(event) => setFieldValue(key, event.currentTarget.value)}
			inputmode="numeric"
			autocomplete="off"
			disabled={saving}
			class="tabular-nums"
			onblur={() => (touched[`${draft.retention}-${key}`] = true)}
			aria-invalid={error ? true : undefined}
			aria-describedby={error ? `${id}-${draft.retention}-${key}-error` : undefined}
		/>
	</div>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		interactOutsideBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save} novalidate>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{editing
								? 'icon-[lucide--pencil]'
								: 'icon-[lucide--calendar-plus]'} text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						{editing ? 'Edit Snapshot Schedule' : 'New Snapshot Schedule'}
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						{editing
							? 'Change how often snapshots are taken and how many are kept.'
							: 'Take snapshots of a file system or volume on a schedule.'}
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
			<ScrollArea
				class="flex min-h-0 flex-auto flex-col"
				fade
				scrollbarYClasses="hidden"
				viewportProps={{ class: 'h-auto min-h-0 w-full flex-auto overscroll-contain' }}
			>
				<div class="space-y-5 p-4">
					{#if editing}
						<div class="space-y-2">
							<div class="flex min-h-5 items-center justify-between gap-2">
								<span class="text-sm leading-none font-medium">File System</span>
								{@render hint(
									'File System',
									'The file system, prefix and Recursive setting can’t be changed. Delete this schedule and create a new one instead.'
								)}
							</div>
							<div class="flex min-w-0 flex-wrap items-center gap-2 rounded-md border px-3 py-2">
								<span class="min-w-0 text-sm break-all">
									{editingName || 'File System Not Found'}<span
										class="text-muted-foreground font-mono text-xs">@{editing.prefix}-…</span
									>
								</span>
								{#if editing.recursive}
									<Badge
										variant="outline"
										class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
									>
										Recursive
									</Badge>
								{/if}
							</div>
						</div>
					{:else}
						<div class="grid gap-4 sm:grid-cols-2">
							<div class="min-w-0">
								<DatasetPicker
									id="{id}-dataset"
									{choices}
									failed={choicesFailed}
									onRetry={onRetryChoices}
									bind:value={draft.dataset}
									disabled={saving}
									error={attempted ? datasetProblem : ''}
								/>
							</div>
							<div class="min-w-0 space-y-2">
								{@render fieldLabel(
									'prefix',
									'Name Prefix',
									shown('prefix', prefixError, draft.prefix),
									`Snapshots are named ${draft.prefix.trim() || 'prefix'}-YYYY-MM-DD-HH-MM, using the time they were taken. Each schedule on a file system needs its own prefix.`
								)}
								<Input
									id="{id}-prefix"
									bind:value={draft.prefix}
									autocomplete="off"
									spellcheck={false}
									disabled={saving}
									class="font-mono text-sm"
									onblur={() => (touched.prefix = true)}
									aria-invalid={shown('prefix', prefixError, draft.prefix) ? true : undefined}
									aria-describedby={shown('prefix', prefixError, draft.prefix)
										? `${id}-prefix-error`
										: undefined}
								/>
							</div>
						</div>
					{/if}
					<div class={['grid gap-4', draft.frequency === CRON && 'sm:grid-cols-2']}>
						<div class="min-w-0 space-y-2">
							{@render fieldLabel(
								'frequency',
								'How Often',
								'',
								'Every Month and Every Year run at midnight on the first day. Use Custom (Cron) for anything else, like weekdays at 3 AM.'
							)}
							<Select.Root type="single" bind:value={draft.frequency} disabled={saving}>
								<Select.Trigger
									id="{id}-frequency"
									class="bg-background w-full dark:hover:bg-input/30"
								>
									<span class="truncate">
										{frequencies.find((choice) => choice.value === draft.frequency)?.label}
									</span>
								</Select.Trigger>
								<Select.Content class="z-60">
									{#each frequencies as choice (choice.value)}
										<Select.Item value={choice.value} label={choice.label} />
									{/each}
								</Select.Content>
							</Select.Root>
						</div>
						{#if draft.frequency === CRON}
							<div class="min-w-0 space-y-2">
								{@render fieldLabel(
									'cron',
									'Cron Expression',
									shown('cron', cronError, draft.cron),
									'Five fields: minute, hour, day of month, month and day of week. For example, 0 3 * * 1-5 runs at 3 AM on weekdays.'
								)}
								<Input
									id="{id}-cron"
									bind:value={draft.cron}
									placeholder="0 3 * * *"
									autocomplete="off"
									spellcheck={false}
									disabled={saving}
									class="font-mono text-sm"
									onblur={() => (touched.cron = true)}
									aria-invalid={shown('cron', cronError, draft.cron) ? true : undefined}
									aria-describedby={shown('cron', cronError, draft.cron)
										? `${id}-cron-error`
										: cronDescription
											? `${id}-cron-description`
											: undefined}
								/>
								{#if cronDescription}
									<p id="{id}-cron-description" class="text-muted-foreground text-xs">
										{cronDescription}
									</p>
								{/if}
							</div>
						{/if}
					</div>
					<section class="space-y-4">
						<div class="min-w-0 space-y-2">
							{@render fieldLabel(
								'retention',
								'Keep',
								attempted ? retentionError : '',
								'Keep Recent keeps the newest snapshots and anything from the last few days. Thin Out Over Time also keeps one per hour, day, week, month and year, so older history is sparser. Snapshots from this schedule outside these limits are deleted after each run. Keep All never deletes them.'
							)}
							<Select.Root type="single" bind:value={draft.retention} disabled={saving}>
								<Select.Trigger
									id="{id}-retention"
									class="bg-background w-full dark:hover:bg-input/30"
								>
									<span class="truncate">
										{retentionChoices.find((choice) => choice.value === draft.retention)?.label}
									</span>
								</Select.Trigger>
								<Select.Content class="z-60">
									{#each retentionChoices as choice (choice.value)}
										<Select.Item value={choice.value} label={choice.label} />
									{/each}
								</Select.Content>
							</Select.Root>
						</div>
						{#if draft.retention === 'simple'}
							<div class="grid gap-4 sm:grid-cols-2">
								{@render countField(
									'keepLast',
									'Keep Last',
									'How many of the newest snapshots to keep.'
								)}
								{@render countField(
									'maxAgeDays',
									'Keep Days',
									'Also keeps every snapshot from this many days back. 0 turns this off.'
								)}
							</div>
						{:else if draft.retention === 'gfs'}
							<div class="grid grid-cols-2 gap-4 sm:grid-cols-3">
								{#each gfsFields as field (field.key)}
									{@render countField(field.key, field.label)}
								{/each}
							</div>
						{/if}
					</section>
					{#if !editing && canRecurse}
						<div class="flex items-center justify-between gap-2">
							<div class="flex items-center gap-2">
								<Checkbox id="{id}-recursive" bind:checked={draft.recursive} disabled={saving} />
								<Label for="{id}-recursive">Recursive</Label>
							</div>
							{@render hint(
								'Recursive',
								'Also snapshots every file system and volume inside it at the same moment.'
							)}
						</div>
					{/if}
					{#if saveError}
						<p class="text-destructive text-sm break-words" role="alert">{saveError}</p>
					{/if}
				</div>
			</ScrollArea>
			<div class="flex shrink-0 items-center justify-end gap-3 border-t p-4">
				<Button
					type="submit"
					class="h-9 gap-2"
					disabled={saving || (attempted && !valid) || (Boolean(editing) && !dirty)}
				>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{saving ? 'Saving…' : editing ? 'Save Changes' : 'Create Schedule'}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
