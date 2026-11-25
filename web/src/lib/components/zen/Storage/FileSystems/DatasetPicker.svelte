<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Command from '$lib/components/ui/command/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { GZFSDatasetTypeSchema, type DetailedDataset } from '$lib/types/zfs/dataset';
	import { isPoolRoot, type PickedDataset } from './filesystems';

	let {
		id,
		choices,
		failed = false,
		onRetry,
		value = $bindable(),
		disabled = false,
		error = ''
	}: {
		id: string;
		choices: DetailedDataset[] | null;
		failed?: boolean;
		onRetry?: () => void;
		value: PickedDataset | undefined;
		disabled?: boolean;
		error?: string;
	} = $props();

	let open = $state(false);

	function icon(entry: DetailedDataset): string {
		if (entry.type === GZFSDatasetTypeSchema.enum.VOLUME)
			return 'icon-[carbon--volume-block-storage]';
		return isPoolRoot(entry) ? 'icon-[bi--hdd-stack-fill]' : 'icon-[eos-icons--file-system]';
	}
</script>

<div class="space-y-2">
	<div class="flex min-h-5 flex-wrap items-baseline gap-x-1">
		<Label for={id}>File System or Volume</Label>
		{#if error}
			<span id="{id}-error" class="text-destructive text-xs">({error})</span>
		{/if}
	</div>
	<Popover.Root bind:open>
		<Popover.Trigger {disabled}>
			{#snippet child({ props })}
				<Button
					{...props}
					{id}
					variant="outline"
					role="combobox"
					aria-expanded={open}
					aria-invalid={error ? true : undefined}
					aria-describedby={error ? `${id}-error` : undefined}
					class="bg-background w-full justify-between px-3 font-normal dark:hover:bg-input/30"
				>
					{#if value}
						<span class="truncate">{value.name}</span>
					{:else}
						<span class="text-muted-foreground">Choose a file system or volume</span>
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
				<Command.Input placeholder="Search file systems and volumes…" />
				<Command.List class="max-h-64">
					{#if choices === null}
						<div class="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
							{#if failed}
								Could not load file systems.
								{#if onRetry}
									<Button variant="link" class="h-auto p-0" onclick={onRetry}>Try Again</Button>
								{/if}
							{:else}
								<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
								></span>
								Loading…
							{/if}
						</div>
					{:else}
						<Command.Empty>No matching file systems or volumes.</Command.Empty>
						{#each choices as entry (entry.name)}
							<Command.Item
								value={entry.name}
								onSelect={() => {
									value = { name: entry.name, guid: entry.guid, type: entry.type };
									open = false;
								}}
							>
								<span class="{icon(entry)} text-muted-foreground size-4 shrink-0" aria-hidden="true"
								></span>
								<span class="min-w-0 truncate">{entry.name}</span>
								{#if value?.name === entry.name}
									<span class="icon-[lucide--check] ml-auto size-4 shrink-0" aria-hidden="true"
									></span>
								{/if}
							</Command.Item>
						{/each}
					{/if}
				</Command.List>
			</Command.Root>
		</Popover.Content>
	</Popover.Root>
</div>
