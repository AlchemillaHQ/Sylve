<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		changeBlock,
		entryIcon,
		entryName,
		isFolder,
		permissionString,
		typeLabel
	} from './explorer';

	let { entry, onClose }: { entry: FileNode; onClose: () => void } = $props();

	type Item = { label: string; value: string; mono?: boolean; warning?: boolean };

	let general = $derived<Item[]>(
		(
			[
				{ label: 'Path', value: entry.id, mono: true },
				{ label: 'Type', value: typeLabel(entry), warning: entry.broken },
				!isFolder(entry) && !entry.special && !entry.broken
					? {
							label: 'Size',
							value: `${formatBytesBinary(entry.size ?? 0)} (${(entry.size ?? 0).toLocaleString()} bytes)`
						}
					: null,
				{ label: 'Modified', value: entry.date.toLocaleString() },
				entry.link ? { label: 'Link Target', value: entry.link, mono: true } : null,
				entry.mount ? { label: 'Mounted Here', value: entry.mount, mono: true } : null
			] as (Item | null)[]
		).filter((item): item is Item => item !== null)
	);
	let access = $derived<Item[]>([
		{ label: 'Owner', value: entry.owner || 'Not Reported' },
		{ label: 'Group', value: entry.group || 'Not Reported' },
		{
			label: 'Permissions',
			value: entry.mode ? `${permissionString(entry)} (${entry.mode})` : 'Not Reported',
			mono: Boolean(entry.mode)
		}
	]);
	let protection = $derived(changeBlock(entry));
</script>

{#snippet section(title: string, entries: Item[])}
	<section class="space-y-2">
		<h3 class="text-sm font-medium">{title}</h3>
		<dl class="grid gap-x-6 gap-y-4 rounded-md border p-4 text-sm sm:grid-cols-2">
			{#each entries as item (item.label)}
				<div class="min-w-0 {item.label === 'Path' ? 'sm:col-span-2' : ''}">
					<dt class="text-muted-foreground text-xs">{item.label}</dt>
					<dd
						class="mt-1 break-all {item.mono ? 'font-mono text-xs leading-5' : ''} {item.warning
							? 'font-medium text-amber-600 dark:text-amber-400'
							: ''}"
					>
						{item.value}
					</dd>
				</div>
			{/each}
		</dl>
	</section>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
		showCloseButton={false}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="{entryIcon(entry)} text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					<span class="min-w-0 break-all">{entryName(entry.id)}</span>
				</Dialog.Title>
				<Dialog.Description class="sr-only">Details of {entry.id}.</Dialog.Description>
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
		<ScrollArea
			class="flex min-h-0 flex-auto flex-col"
			fade
			scrollbarYClasses="hidden"
			viewportProps={{ class: 'h-auto min-h-0 w-full flex-auto overscroll-contain' }}
		>
			<div class="space-y-5 p-4">
				{@render section('General', general)}
				{@render section('Access', access)}
				{#if protection}
					<section class="space-y-2">
						<h3 class="text-sm font-medium">Protection</h3>
						<p class="rounded-md border p-4 text-sm leading-relaxed">
							{protection} It can’t be renamed, moved or deleted here.
						</p>
					</section>
				{/if}
			</div>
		</ScrollArea>
	</Dialog.Content>
</Dialog.Root>
