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
	import { accountProblem } from './nfs';

	let {
		id,
		kind,
		value,
		options,
		loading = false,
		optional = false,
		disabled = false,
		invalid = false,
		describedBy,
		onChange
	}: {
		id: string;
		kind: 'user' | 'group';
		value: string;
		options: string[];
		loading?: boolean;
		optional?: boolean;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		onChange: (value: string) => void;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let typed = $derived(query.trim());
	let custom = $derived(
		typed && !options.includes(typed) && typed !== value && !accountProblem(typed) ? typed : ''
	);
	let choices = $derived(value && !options.includes(value) ? [value, ...options] : options);

	function choose(next: string) {
		onChange(next);
		query = '';
		open = false;
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
				<span class="truncate {value ? '' : 'text-muted-foreground'}">
					{value || (optional ? 'Default Group' : `Choose a ${kind}`)}
				</span>
				<span class="icon-[lucide--chevrons-up-down] size-4 shrink-0 opacity-50" aria-hidden="true"
				></span>
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content
		class="z-60 w-(--bits-popover-anchor-width) min-w-56 p-0"
		align="start"
		sideOffset={4}
	>
		<Command.Root>
			<Command.Input
				bind:value={query}
				placeholder={kind === 'user' ? 'Search or type a user…' : 'Search or type a group…'}
			/>
			<Command.List class="max-h-64">
				<Command.Empty>
					{loading ? 'Loading…' : `Type a ${kind} name to use it.`}
				</Command.Empty>
				{#if custom}
					<Command.Item value="custom {custom}" onSelect={() => choose(custom)}>
						<span
							class="icon-[lucide--plus] text-muted-foreground size-4 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 flex-1 truncate">Use “{custom}”</span>
					</Command.Item>
				{/if}
				{#if optional && value}
					<Command.Item value="none default" onSelect={() => choose('')}>
						<span
							class="icon-[lucide--circle-minus] text-muted-foreground size-4 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 flex-1 truncate">Default Group</span>
					</Command.Item>
				{/if}
				{#each choices as option (option)}
					<Command.Item value={option} onSelect={() => choose(option)}>
						<span
							class="{kind === 'user'
								? 'icon-[lucide--user]'
								: 'icon-[lucide--users]'} text-muted-foreground size-4 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 flex-1 truncate">{option}</span>
						{#if option === value}
							<span class="icon-[lucide--check] size-4 shrink-0" aria-hidden="true"></span>
							<span class="sr-only">(selected)</span>
						{/if}
					</Command.Item>
				{/each}
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
