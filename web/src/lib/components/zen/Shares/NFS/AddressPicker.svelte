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
	import * as Popover from '$lib/components/ui/popover/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { bindAddressProblem, normalizedAddress, type AddressOption } from './nfs';

	let {
		id,
		options,
		value,
		loading = false,
		failed = false,
		disabled = false,
		invalid = false,
		describedBy,
		onRetry,
		onChange
	}: {
		id: string;
		options: AddressOption[];
		value: string[];
		loading?: boolean;
		failed?: boolean;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		onRetry: () => void;
		onChange: (next: string[]) => void;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let byValue = $derived(new Map(options.map((option) => [option.value, option])));
	let known = $derived(!loading && !failed);
	let typed = $derived(query.trim() ? normalizedAddress(query.trim()) : '');
	let custom = $derived(
		typed && !byValue.has(typed) && !value.includes(typed) && !bindAddressProblem(typed)
			? typed
			: ''
	);

	function toggle(address: string) {
		onChange(
			value.includes(address) ? value.filter((entry) => entry !== address) : [...value, address]
		);
	}
</script>

<Popover.Root
	bind:open
	onOpenChange={(next) => {
		if (!next) query = '';
	}}
>
	<Popover.Trigger {disabled}>
		{#snippet child({ props })}
			<Button
				{...props}
				{id}
				variant="outline"
				role="combobox"
				aria-expanded={open}
				aria-invalid={invalid || undefined}
				aria-describedby={describedBy}
				class="bg-background aria-invalid:border-destructive dark:aria-invalid:border-destructive w-full justify-between px-3 font-normal dark:hover:bg-input/30"
			>
				<span class="text-muted-foreground truncate">
					{value.length ? `${value.length} selected` : 'All Addresses'}
				</span>
				<span class="icon-[lucide--chevrons-up-down] size-4 shrink-0 opacity-50" aria-hidden="true"
				></span>
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content
		class="z-60 w-(--bits-popover-anchor-width) min-w-72 p-0"
		align="start"
		sideOffset={4}
	>
		<Command.Root>
			<Command.Input bind:value={query} placeholder="Search or type an address…" />
			<Command.List class="max-h-72">
				<Command.Empty>
					{#if failed}
						<span class="flex flex-col items-center gap-3">
							Addresses couldn’t be loaded.
							<Button variant="outline" size="sm" onclick={onRetry}>Try Again</Button>
						</span>
					{:else if loading}
						Loading addresses…
					{:else}
						No matching addresses.
					{/if}
				</Command.Empty>
				{#if custom}
					<Command.Item value="custom {custom}" onSelect={() => toggle(custom)}>
						<span
							class="icon-[lucide--plus] text-muted-foreground size-4 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 flex-1 truncate font-mono">Use {custom}</span>
					</Command.Item>
				{/if}
				{#each options as option (option.value)}
					{@const chosen = value.includes(option.value)}
					<Command.Item value="{option.value} {option.label}" onSelect={() => toggle(option.value)}>
						<span
							class="icon-[mdi--ethernet] text-muted-foreground size-4 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 flex-1">
							<span class="block truncate font-mono">{option.value}</span>
							<span class="text-muted-foreground block truncate text-xs">{option.label}</span>
						</span>
						{#if chosen}
							<span class="icon-[lucide--check] size-4 shrink-0" aria-hidden="true"></span>
							<span class="sr-only">(selected)</span>
						{/if}
					</Command.Item>
				{/each}
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
{#if value.length}
	<ul class="mt-2 flex flex-wrap gap-1.5" aria-label="Selected addresses">
		{#each value as address (address)}
			{@const missing = known && !byValue.has(address)}
			<li
				class="bg-muted/40 inline-flex max-w-full items-center gap-1.5 rounded-md border py-0.5 pr-0.5 pl-2 font-mono text-xs"
			>
				<span class="truncate">{address}</span>
				{#if missing}
					<Tooltip.Root delayDuration={500}>
						<Tooltip.Trigger
							type="button"
							class="focus-visible:ring-ring inline-flex shrink-0 cursor-help items-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
						>
							<span
								class="icon-[lucide--triangle-alert] size-3.5 text-amber-600 dark:text-amber-400"
								aria-hidden="true"
							></span>
							<span class="sr-only">Not found on this node</span>
						</Tooltip.Trigger>
						<Tooltip.Content side="top" sideOffset={6} class="max-w-64 text-left font-sans">
							No interface on this node has this address
						</Tooltip.Content>
					</Tooltip.Root>
				{/if}
				<button
					type="button"
					class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
					aria-label="Remove {address}"
					{disabled}
					onclick={() => toggle(address)}
				>
					<span class="icon-[lucide--x] size-3.5" aria-hidden="true"></span>
				</button>
			</li>
		{/each}
	</ul>
{/if}
