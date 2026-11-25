<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { createSnapshot } from '$lib/api/zfs/datasets';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { DetailedDataset } from '$lib/types/zfs/dataset';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { snapshotCreateErrorMessage } from '$lib/utils/zfs/snapshot';
	import { defaultSnapshotName, snapshotNameProblem } from './actions';
	import DatasetPicker from './DatasetPicker.svelte';
	import { isVolume, type PickedDataset } from './filesystems';

	let {
		hostname,
		dataset: fixed,
		choices = null,
		choicesFailed = false,
		onRetryChoices,
		onClose,
		onDone
	}: {
		hostname: string;
		dataset?: PickedDataset;
		choices?: DetailedDataset[] | null;
		choicesFailed?: boolean;
		onRetryChoices?: () => void;
		onClose: () => void;
		onDone?: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const preset = untrack(() => fixed);
	let chosen = $state<PickedDataset | undefined>(preset);
	let name = $state(defaultSnapshotName());
	let recursive = $state(false);
	let canRecurse = $derived(!isVolume(chosen));
	let touched = $state(false);
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let datasetProblem = $derived(chosen ? '' : 'Choose a file system or volume');
	let problem = $derived(snapshotNameProblem(name, chosen?.name ?? ''));
	let nameError = $derived(attempted || (touched && name.trim()) ? problem : '');

	onDestroy(() => lifetime.abort());

	async function save(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (saving || problem || !chosen) return;
		saving = true;
		saveError = '';
		const target = chosen;
		const snapshot = name.trim();
		try {
			const response = await createSnapshot(target, snapshot, canRecurse && recursive, {
				hostname,
				signal: lifetime.signal
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(`Snapshot ${target.name}@${snapshot} created`, {
				position: 'bottom-center'
			});
			onDone?.();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = snapshotCreateErrorMessage(
				error instanceof Error ? error.message : 'The snapshot could not be created.',
				snapshot
			);
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
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
							class="icon-[lucide--camera] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">
							{preset ? `Take Snapshot — ${preset.name}` : 'Take Snapshot'}
						</span>
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						{preset
							? `Create a point-in-time snapshot of ${preset.name}.`
							: 'Create a point-in-time snapshot of a file system or volume.'}
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
				{#if !preset}
					<DatasetPicker
						id="{id}-dataset"
						{choices}
						failed={choicesFailed}
						onRetry={onRetryChoices}
						bind:value={chosen}
						disabled={saving}
						error={attempted ? datasetProblem : ''}
					/>
				{/if}
				<div class="space-y-2">
					<div class="flex min-h-5 flex-wrap items-baseline gap-x-1">
						<Label for="{id}-name">Name</Label>
						{#if nameError}
							<span id="{id}-name-error" class="text-destructive text-xs">({nameError})</span>
						{/if}
					</div>
					<Input
						id="{id}-name"
						bind:value={name}
						autocomplete="off"
						spellcheck={false}
						disabled={saving}
						class="font-mono text-sm"
						onblur={() => (touched = true)}
						aria-invalid={nameError ? true : undefined}
						aria-describedby={nameError ? `${id}-name-error` : undefined}
					/>
				</div>
				{#if canRecurse}
					<div class="flex items-center justify-between gap-2">
						<div class="flex items-center gap-2">
							<Checkbox id="{id}-recursive" bind:checked={recursive} disabled={saving} />
							<Label for="{id}-recursive">Recursive</Label>
						</div>
						<span id="{id}-recursive-help" class="sr-only">
							Also snapshots every file system and volume inside it at the same moment.
						</span>
						<Tooltip.Root delayDuration={150}>
							<Tooltip.Trigger
								type="button"
								aria-label="Help for Recursive"
								class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
							>
								<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
							</Tooltip.Trigger>
							<Tooltip.Content
								side="top"
								align="end"
								sideOffset={6}
								class="z-60 max-w-64 text-left leading-5"
							>
								Also snapshots every file system and volume inside it at the same moment.
							</Tooltip.Content>
						</Tooltip.Root>
					</div>
				{/if}
				{#if saveError}
					<p class="text-destructive text-sm break-words" role="alert">{saveError}</p>
				{/if}
			</div>
			<div class="flex shrink-0 items-center justify-end gap-3 border-t p-4">
				<Button
					type="submit"
					class="h-9 gap-2"
					disabled={saving || (attempted && Boolean(problem || datasetProblem))}
				>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{saving ? 'Taking Snapshot…' : 'Take Snapshot'}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
