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
	import type { DatasetChoice } from './shared';

	let {
		id,
		value,
		label,
		placeholder,
		choices,
		loading = false,
		failed = false,
		disabled = false,
		invalid = false,
		describedBy,
		onRetry,
		onChange
	}: {
		id: string;
		value: string;
		label: string;
		placeholder: string;
		choices: DatasetChoice[];
		loading?: boolean;
		failed?: boolean;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		onRetry: () => void;
		onChange: (guid: string) => void;
	} = $props();

	let open = $state(false);
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
				aria-invalid={invalid || undefined}
				aria-describedby={describedBy}
				class="bg-background aria-invalid:border-destructive dark:aria-invalid:border-destructive w-full justify-between px-3 font-normal dark:hover:bg-input/30"
			>
				<span class="truncate {label ? '' : 'text-muted-foreground'}">{label || placeholder}</span>
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
				<p class="text-muted-foreground">Datasets couldn’t be loaded.</p>
				<Button variant="outline" size="sm" onclick={onRetry}>Try Again</Button>
			</div>
		{:else if loading}
			<p class="text-muted-foreground flex items-center justify-center gap-2 p-4 text-sm">
				<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				Loading datasets…
			</p>
		{:else}
			<Command.Root>
				<Command.Input placeholder="Search datasets…" />
				<Command.List class="max-h-72">
					<Command.Empty>No matching datasets.</Command.Empty>
					{#each choices as choice (choice.guid)}
						<Command.Item
							value="{choice.name} {choice.mountpoint} {choice.guid}"
							disabled={Boolean(choice.reason)}
							onSelect={() => {
								onChange(choice.guid);
								open = false;
							}}
						>
							<span
								class="{choice.name.includes('/')
									? 'icon-[eos-icons--file-system]'
									: 'icon-[bi--hdd-stack-fill]'} text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>
							<span class="min-w-0 flex-1">
								<span class="block truncate">{choice.name}</span>
								<span
									class="text-muted-foreground block truncate text-xs {choice.reason
										? ''
										: 'font-mono'}"
								>
									{choice.reason || choice.mountpoint}
								</span>
							</span>
							{#if value === choice.guid}
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
