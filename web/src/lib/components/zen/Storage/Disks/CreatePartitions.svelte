<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { createPartitions } from '$lib/api/disk/disk';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { Disk } from '$lib/types/disk/disk';
	import {
		formatBytesBinary,
		normalizeSizeInputExact,
		parseSizeInputToBytes
	} from '$lib/utils/bytes';
	import { handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { diskActionError, maxPartitions, MiB, partitionSpace, partitionTypes } from './disks';

	let {
		open = $bindable(),
		disk,
		hostname,
		canModify,
		onChanged
	}: {
		open: boolean;
		disk: Disk;
		hostname: string;
		canModify: boolean;
		onChanged: () => Promise<void>;
	} = $props();

	const id = $props.id();
	const failed = 'Could not create the partitions. Please try again.';
	let destroyed = false;
	onDestroy(() => (destroyed = true));

	type Draft = { key: number; size: string; type: string };
	let nextKey = 1;
	let drafts = $state<Draft[]>([{ key: 0, size: '', type: 'freebsd-zfs' }]);
	let saving = $state(false);
	let error = $state('');

	let sizes = $derived(
		drafts.map((draft) => {
			const bytes = parseSizeInputToBytes(draft.size);
			return bytes === null ? null : Math.floor(bytes / MiB) * MiB;
		})
	);
	let existing = $derived(disk.partitions.reduce((total, partition) => total + partition.size, 0));
	let available = $derived(partitionSpace(disk));
	let requested = $derived(sizes.reduce<number>((total, size) => total + (size ?? 0), 0));
	let remaining = $derived(available - requested);
	let entriesLeft = $derived(Math.max(0, maxPartitions - disk.partitions.length));
	let valid = $derived(
		drafts.length > 0 &&
			drafts.length <= entriesLeft &&
			sizes.every((size) => size !== null && size >= MiB) &&
			remaining >= 0
	);
	let touched = $state<Record<number, boolean>>({});
	let rowHeights = $state<Record<number, number>>({});
	let listHeight = $derived.by(() => {
		if (drafts.length <= 3) return undefined;
		const heights = drafts.slice(0, 3).map((draft) => rowHeights[draft.key]);
		return heights.every((height) => height > 0)
			? `calc(${heights[0] + heights[1] + heights[2]}px + 1.5rem)`
			: '24rem';
	});

	function sizeMessage(input: string, size: number | null): string {
		if (!input.trim()) return '';
		if (size === null) return 'Use a format like 20G';
		if (size < MiB) return 'Must be at least 1 MiB';
		return '';
	}

	function fillSize(index: number): number {
		return remaining + (sizes[index] ?? 0);
	}

	function fill(index: number) {
		const size = fillSize(index);
		if (size >= MiB) drafts[index].size = normalizeSizeInputExact(size) ?? '';
	}

	async function add() {
		const key = nextKey++;
		drafts.push({ key, size: '', type: 'freebsd-zfs' });
		await tick();
		document
			.getElementById(`${id}-row-${key}`)
			?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
		document.getElementById(`${id}-size-${key}`)?.focus({ preventScroll: true });
	}

	function remove(index: number) {
		drafts.splice(index, 1);
	}

	function typeLabel(value: string): string {
		return partitionTypes.find((type) => type.value === value)?.label ?? value;
	}

	async function create(event: SubmitEvent) {
		event.preventDefault();
		if (saving || !valid || !canModify) return;
		const request = sizes.filter((size) => size !== null);
		saving = true;
		error = '';
		try {
			const response = await createPartitions(
				disk.device,
				request,
				drafts.map((draft) => draft.type),
				{ hostname }
			);
			if (destroyed) return;
			if (response.status !== 'success') {
				error = diskActionError(response, failed);
				if (request.length > 1)
					error += ' Any partitions created before the error remain on the disk.';
				handleAPIError(response);
				await onChanged();
				return;
			}
			open = false;
			toast.success(
				`Created ${request.length} ${request.length === 1 ? 'partition' : 'partitions'} on ${disk.device}`,
				{ position: 'bottom-center' }
			);
			await onChanged();
		} catch (caught) {
			if (!isRequestCancellation(caught) && !destroyed) error = failed;
		} finally {
			if (!destroyed) saving = false;
		}
	}
</script>

{#snippet hint(label: string, description: string, helpId: string)}
	<span id={helpId} class="sr-only">{description}</span>
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {label}"
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
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet swatch(tone: string)}
	<span class="inline-block size-2 shrink-0 rounded-full {tone}" aria-hidden="true"></span>
{/snippet}

<Dialog.Root
	{open}
	onOpenChange={(value) => {
		if (!saving) open = value;
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={create}>
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="icon-[ant-design--partition-outlined] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">Create Partitions — {disk.device}</span>
					</Dialog.Title>
					<Dialog.Description class="text-xs">
						Add partitions in the free space on this disk, on {hostname}.
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
			<div class="min-h-0 space-y-5 overflow-y-auto p-4">
				<section class="space-y-2" aria-label="Disk space">
					<div class="bg-muted flex h-2.5 gap-px overflow-hidden rounded-full" aria-hidden="true">
						{#if existing > 0}
							<div
								class="bg-muted-foreground/40 min-w-0.5"
								style:width="{(existing / disk.size) * 100}%"
							></div>
						{/if}
						{#each drafts as draft, index (draft.key)}
							{@const size = sizes[index]}
							{#if size && size > 0}
								<div
									class="min-w-0.5 {remaining < 0
										? index % 2
											? 'bg-destructive/60'
											: 'bg-destructive'
										: index % 2
											? 'bg-zen-accent/60'
											: 'bg-zen-accent'}"
									style:width="{(size / disk.size) * 100}%"
								></div>
							{/if}
						{/each}
					</div>
					<dl class="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
						<div class="flex items-center gap-1.5">
							{@render swatch('bg-muted-foreground/40')}
							<dt>Existing</dt>
							<dd class="text-foreground tabular-nums">{formatBytesBinary(existing)}</dd>
						</div>
						<div class="flex items-center gap-1.5">
							{@render swatch(remaining < 0 ? 'bg-destructive' : 'bg-zen-accent')}
							<dt>New</dt>
							<dd class="text-foreground tabular-nums">{formatBytesBinary(requested)}</dd>
						</div>
						<div class="flex items-center gap-1.5">
							{@render swatch('bg-muted border')}
							<dt>Free</dt>
							<dd class="text-foreground tabular-nums">
								{formatBytesBinary(Math.max(0, remaining))}
							</dd>
						</div>
					</dl>
				</section>

				<div class="space-y-3">
					<ScrollArea
						class="min-w-0 rounded-md"
						fade
						fadeClasses="from-background"
						scrollbarYClasses="hidden"
						viewportProps={{
							class: 'overscroll-contain',
							style: listHeight ? `max-height: ${listHeight};` : undefined,
							role: 'region',
							'aria-label': 'New partitions',
							tabindex: 0
						}}
					>
						<ol class="space-y-3">
							{#each drafts as draft, index (draft.key)}
								{@const message = touched[draft.key] ? sizeMessage(draft.size, sizes[index]) : ''}
								{@const fillable = fillSize(index)}
								<li
									id="{id}-row-{draft.key}"
									bind:offsetHeight={rowHeights[draft.key]}
									class="rounded-md border p-3"
								>
									<div class="mb-2 flex items-center justify-between gap-2">
										<span class="text-sm font-medium">New Partition {index + 1}</span>
										<Button
											type="button"
											variant="ghost"
											size="icon"
											class="-my-1.5 -mr-1.5 size-8"
											disabled={saving || drafts.length === 1}
											aria-label="Remove new partition {index + 1}"
											onclick={() => remove(index)}
										>
											<span class="icon-[lucide--x] size-4" aria-hidden="true"></span>
										</Button>
									</div>
									<div class="grid gap-3 sm:grid-cols-2">
										<div class="min-w-0 space-y-1.5">
											<div class="flex min-h-5 items-center justify-between gap-2">
												<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
													<Label for="{id}-size-{draft.key}" class="text-xs">Size</Label>
													{#if message}
														<span
															id="{id}-size-{draft.key}-error"
															class="text-destructive text-xs leading-4"
														>
															({message})
														</span>
													{/if}
												</div>
												{@render hint(
													'Size',
													'Sizes use binary units: 512M is 512 MiB and 20G is 20 GiB. Each size is rounded down to whole MiB.',
													`${id}-size-${draft.key}-help`
												)}
											</div>
											<div class="relative">
												<Input
													id="{id}-size-{draft.key}"
													class="pr-10"
													bind:value={draft.size}
													placeholder="e.g. 20G"
													autocomplete="off"
													spellcheck={false}
													disabled={saving}
													onblur={() => (touched[draft.key] = true)}
													aria-invalid={message ? true : undefined}
													aria-describedby="{id}-size-{draft.key}-help{message
														? ` ${id}-size-${draft.key}-error`
														: ''}"
												/>
												<Tooltip.Root delayDuration={150}>
													<Tooltip.Trigger
														type="button"
														class="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-ring absolute top-1/2 right-1 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50"
														disabled={saving || fillable < MiB}
														aria-label="Use all remaining space for new partition {index + 1}"
														onclick={() => fill(index)}
													>
														<span
															class="icon-[lucide--arrow-right-to-line] size-4"
															aria-hidden="true"
														></span>
													</Tooltip.Trigger>
													<Tooltip.Content side="top" align="end" sideOffset={6} class="z-60">
														Use remaining space ({formatBytesBinary(fillable)})
													</Tooltip.Content>
												</Tooltip.Root>
											</div>
										</div>
										<div class="min-w-0 space-y-1.5">
											<div class="flex min-h-5 items-center justify-between gap-2">
												<Label for="{id}-type-{draft.key}" class="text-xs">Type</Label>
												{@render hint(
													'Type',
													'Only FreeBSD ZFS partitions can be used in pools. A swap partition also keeps every other partition on this disk out of pools.',
													`${id}-type-${draft.key}-help`
												)}
											</div>
											<Select.Root type="single" bind:value={draft.type} disabled={saving}>
												<Select.Trigger
													id="{id}-type-{draft.key}"
													class="bg-background w-full dark:hover:bg-input/30"
													aria-describedby="{id}-type-{draft.key}-help"
												>
													{typeLabel(draft.type)}
												</Select.Trigger>
												<Select.Content class="z-60">
													{#each partitionTypes as type (type.value)}
														<Select.Item value={type.value} label={type.label} />
													{/each}
												</Select.Content>
											</Select.Root>
										</div>
									</div>
								</li>
							{/each}
						</ol>
					</ScrollArea>
					<Button
						type="button"
						variant="outline"
						size="sm"
						class="h-9 gap-2"
						disabled={saving || drafts.length >= entriesLeft || remaining < MiB}
						onclick={add}
					>
						<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>Add Partition
					</Button>
				</div>

				{#if !canModify}
					<p class="text-destructive text-sm" role="alert">
						You do not have permission to modify disks on this node.
					</p>
				{/if}
				{#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
			</div>
			<div class="flex shrink-0 items-center gap-3 border-t p-4">
				{#if remaining < 0}
					<p class="text-destructive min-w-0 text-sm" role="status">
						{formatBytesBinary(-remaining)} more than the free space
					</p>
				{/if}
				<Button
					type="submit"
					class="ml-auto h-9 shrink-0 gap-2"
					disabled={saving || !valid || !canModify}
				>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{saving
						? 'Creating…'
						: drafts.length === 1
							? 'Create Partition'
							: `Create ${drafts.length} Partitions`}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
