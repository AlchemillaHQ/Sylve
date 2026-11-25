<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getDownloadsResult } from '$lib/api/utilities/downloader';
	import { flashVolume } from '$lib/api/zfs/datasets';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Command from '$lib/components/ui/command/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { DetailedDataset } from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		APIRequestError,
		handleAPIError,
		isAPIResponse,
		isRequestCancellation
	} from '$lib/utils/http';
	import { getISOs, type DownloadImageOption } from '$lib/utils/utilities/downloader';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { flashErrorMessage, volumeSize } from './volumes';

	let {
		hostname,
		volume,
		onClose,
		onDone
	}: {
		hostname: string;
		volume: DetailedDataset;
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const target = untrack(() => ({
		name: volume.name,
		guid: volume.guid,
		size: volumeSize(volume)
	}));

	let images = $state<DownloadImageOption[] | null>(null);
	let loadFailed = $state(false);
	let pickerOpen = $state(false);
	let chosen = $state('');
	let typed = $state('');
	let attempted = $state(false);
	let flashing = $state(false);
	let flashError = $state('');
	let image = $derived(images?.find((option) => option.value === chosen));
	let imageProblem = $derived(image ? '' : 'Choose an image');
	let confirmed = $derived(typed.trim() === target.name);

	async function loadImages() {
		images = null;
		loadFailed = false;
		try {
			const result = await getDownloadsResult({ hostname, signal: lifetime.signal });
			if (isAPIResponse(result)) throw new APIRequestError(result);
			images = getISOs(result, true);
		} catch (error) {
			if (!lifetime.signal.aborted && !isRequestCancellation(error)) loadFailed = true;
		}
	}

	void loadImages();
	onDestroy(() => lifetime.abort());

	function kindLabel(option: DownloadImageOption): string {
		return option.kind === 'iso' ? 'ISO' : 'Disk Image';
	}

	async function flash(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (flashing || !image || !confirmed) return;
		flashing = true;
		flashError = '';
		const source = image.label;
		try {
			const response = await flashVolume(target.guid, image.value, { hostname });
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(`${source} written to ${target.name}`, { position: 'bottom-center' });
			if (lifetime.signal.aborted) return;
			onDone();
			onClose();
		} catch (error) {
			const message = flashErrorMessage(
				error instanceof Error ? error.message : 'The image could not be written.'
			);
			if (lifetime.signal.aborted) {
				toast.error(`Writing ${source} to ${target.name} failed: ${message}`, {
					position: 'bottom-center'
				});
				return;
			}
			flashError = message;
			onDone();
		} finally {
			if (!lifetime.signal.aborted) flashing = false;
		}
	}
</script>

{#snippet hint(label: string, description: string)}
	{@const helpId = `${id}-${label.toLowerCase().replaceAll(' ', '-')}-help`}
	<span id={helpId} class="sr-only">{description}</span>
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {label}"
			aria-describedby={helpId}
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

{#snippet imagePicker()}
	{@const error = attempted ? imageProblem : ''}
	<div class="min-w-0 space-y-2">
		<div class="flex min-h-5 items-center justify-between gap-2">
			<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
				<Label for="{id}-image">Image</Label>
				{#if error}
					<span id="{id}-image-error" class="text-destructive text-xs">({error})</span>
				{/if}
			</div>
			{@render hint(
				'Image',
				`An ISO or disk image from Downloads. Disk images in formats such as QCOW2 or VMDK are converted to raw as they’re written. It must fit in the volume’s ${formatBytesBinary(target.size)}.`
			)}
		</div>
		<Popover.Root bind:open={pickerOpen}>
			<Popover.Trigger disabled={flashing}>
				{#snippet child({ props })}
					<Button
						{...props}
						id="{id}-image"
						variant="outline"
						role="combobox"
						aria-expanded={pickerOpen}
						aria-invalid={error ? true : undefined}
						aria-describedby={error ? `${id}-image-error` : undefined}
						class="bg-background w-full justify-between px-3 font-normal dark:hover:bg-input/30"
					>
						{#if image}
							<span class="flex min-w-0 items-center gap-2">
								<span
									class="{image.kind === 'iso'
										? 'icon-[lucide--disc-3]'
										: 'icon-[lucide--hard-drive]'} text-muted-foreground size-4 shrink-0"
									aria-hidden="true"
								></span>
								<span class="truncate">{image.label}</span>
							</span>
						{:else}
							<span class="text-muted-foreground">Choose an image</span>
						{/if}
						<span
							class="icon-[lucide--chevrons-up-down] size-4 shrink-0 opacity-50"
							aria-hidden="true"
						></span>
					</Button>
				{/snippet}
			</Popover.Trigger>
			<Popover.Content
				class="z-60 w-(--bits-popover-anchor-width) min-w-64 p-0"
				align="start"
				sideOffset={4}
			>
				<Command.Root>
					<Command.Input placeholder="Search images…" />
					<Command.List class="max-h-64">
						{#if images === null}
							<div class="text-muted-foreground flex items-center gap-2 px-3 py-6 text-sm">
								{#if loadFailed}
									<span class="min-w-0 flex-1">Downloads could not be loaded.</span>
									<Button variant="outline" size="sm" onclick={loadImages}>Try Again</Button>
								{:else}
									<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
									></span>
									Loading…
								{/if}
							</div>
						{:else if images.length === 0}
							<p class="text-muted-foreground px-3 py-6 text-center text-sm">
								No ISO or disk images have been downloaded on this node.
							</p>
						{:else}
							<Command.Empty>No matching images.</Command.Empty>
							{#each images as option (option.value)}
								<Command.Item
									value={option.value}
									keywords={[option.label]}
									disabled={option.disabled}
									onSelect={() => {
										chosen = option.value;
										pickerOpen = false;
									}}
								>
									<span
										class="{option.kind === 'iso'
											? 'icon-[lucide--disc-3]'
											: 'icon-[lucide--hard-drive]'} text-muted-foreground size-4 shrink-0"
										aria-hidden="true"
									></span>
									<span class="min-w-0 truncate">{option.label}</span>
									<span class="text-muted-foreground ml-auto shrink-0 text-xs">
										{option.disabled ? option.description : kindLabel(option)}
									</span>
									{#if chosen === option.value}
										<span class="icon-[lucide--check] size-4 shrink-0" aria-hidden="true"></span>
									{/if}
								</Command.Item>
							{/each}
						{/if}
					</Command.List>
				</Command.Root>
			</Popover.Content>
		</Popover.Root>
	</div>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !flashing) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-lg"
		showCloseButton={false}
		escapeKeydownBehavior={flashing ? 'ignore' : 'close'}
		interactOutsideBehavior={flashing ? 'ignore' : 'close'}
		aria-busy={flashing}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={flash} novalidate>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="icon-[lucide--hard-drive-download] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">Flash Image — {target.name}</span>
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						Write a downloaded ISO or disk image onto {target.name}.
					</Dialog.Description>
				</Dialog.Header>
				<Dialog.Close disabled={flashing}>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost"
							size="icon"
							class="size-9 shrink-0"
							disabled={flashing}
						>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</Dialog.Close>
			</div>
			<div class="min-h-0 space-y-4 overflow-y-auto p-4">
				{@render imagePicker()}
				<div class="border-destructive/40 space-y-3 rounded-md border p-3">
					<p class="text-sm leading-relaxed">
						Flashing writes the image over the start of
						<span class="text-foreground font-semibold break-words">{target.name}</span>. Anything
						stored there now is lost. This cannot be undone.
					</p>
					<div class="space-y-2">
						<Label for="{id}-confirm" class="block leading-relaxed">
							Type <span class="text-foreground text-sm font-semibold break-words"
								>{target.name}</span
							> to confirm
						</Label>
						<Input
							id="{id}-confirm"
							bind:value={typed}
							placeholder={target.name}
							autocomplete="off"
							spellcheck={false}
							disabled={flashing}
						/>
					</div>
				</div>
				{#if flashing}
					<p class="text-muted-foreground text-sm leading-relaxed" role="status">
						Writing the image. Large images can take several minutes, and the write keeps going if
						you leave this page.
					</p>
				{/if}
				{#if flashError}
					<p class="text-destructive text-sm break-words" role="alert">{flashError}</p>
				{/if}
			</div>
			<div class="flex shrink-0 items-center justify-end gap-3 border-t p-4">
				<Button
					type="submit"
					variant="destructive"
					class="h-9 gap-2"
					disabled={flashing || !confirmed || (attempted && Boolean(imageProblem))}
				>
					{#if flashing}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{flashing ? 'Flashing…' : 'Flash Image'}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
