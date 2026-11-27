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
	import type { HostInterfaceOption } from '$lib/utils/network/helpers';

	let {
		id,
		options,
		value,
		loading = false,
		failed = false,
		disabled = false,
		error = '',
		onRetry,
		onToggle
	}: {
		id: string;
		options: HostInterfaceOption[];
		value: string[];
		loading?: boolean;
		failed?: boolean;
		disabled?: boolean;
		error?: string;
		onRetry: () => void;
		onToggle: (name: string) => void;
	} = $props();

	let open = $state(false);
	let byValue = $derived(new Map(options.map((option) => [option.value, option])));
	let known = $derived(!loading && !failed);
</script>

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
				class="bg-background aria-invalid:border-destructive dark:aria-invalid:border-destructive w-full justify-between px-3 font-normal dark:hover:bg-input/30"
			>
				<span class="text-muted-foreground truncate">
					{value.length ? `${value.length} selected` : 'Choose interfaces'}
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
		{#if failed}
			<div class="flex flex-col items-center gap-3 p-4 text-center text-sm">
				<p class="text-muted-foreground">Interfaces couldn’t be loaded.</p>
				<Button variant="outline" size="sm" onclick={onRetry}>Try Again</Button>
			</div>
		{:else if loading}
			<p class="text-muted-foreground flex items-center justify-center gap-2 p-4 text-sm">
				<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				Loading interfaces…
			</p>
		{:else}
			<Command.Root>
				<Command.Input placeholder="Search interfaces…" />
				<Command.List class="max-h-72">
					<Command.Empty>No matching interfaces.</Command.Empty>
					{#each options as option (option.value)}
						{@const chosen = value.includes(option.value)}
						<Command.Item
							value="{option.label} {option.value}"
							disabled={option.disabled && !chosen}
							onSelect={() => onToggle(option.value)}
						>
							<span
								class="icon-[mdi--ethernet] text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>
							<span class="min-w-0 flex-1">
								<span class="block truncate">{option.label}</span>
								{#if option.description}
									<span class="text-muted-foreground block truncate text-xs"
										>{option.description}</span
									>
								{/if}
							</span>
							{#if chosen}
								<span class="icon-[lucide--check] size-4 shrink-0" aria-hidden="true"></span>
								<span class="sr-only">(selected)</span>
							{/if}
						</Command.Item>
					{/each}
				</Command.List>
			</Command.Root>
		{/if}
	</Popover.Content>
</Popover.Root>
{#if value.length}
	<ul class="mt-2 flex flex-wrap gap-1.5" aria-label="Selected interfaces">
		{#each value as name (name)}
			{@const option = byValue.get(name)}
			{@const missing = known && !option}
			<li
				class="bg-muted/40 inline-flex max-w-full items-center gap-1.5 rounded-md border py-0.5 pr-0.5 pl-2 text-xs"
			>
				<span class="truncate">{option?.label ?? name}</span>
				{#if missing}
					<span
						class="icon-[lucide--triangle-alert] size-3.5 shrink-0 text-amber-600 dark:text-amber-400"
						title="Not found on this node"
						aria-hidden="true"
					></span>
					<span class="sr-only">(Not found on this node)</span>
				{/if}
				<button
					type="button"
					class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
					aria-label="Remove {option?.label ?? name}"
					{disabled}
					onclick={() => onToggle(name)}
				>
					<span class="icon-[lucide--x] size-3.5" aria-hidden="true"></span>
				</button>
			</li>
		{/each}
	</ul>
{/if}
