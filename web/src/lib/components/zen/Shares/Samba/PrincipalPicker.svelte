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
	import { watch } from 'runed';
	import type { PrincipalOption } from './samba';

	let {
		id,
		label,
		placeholder,
		options,
		value,
		notes = {},
		disabled = false,
		invalid = false,
		describedBy,
		searching = false,
		searchError = '',
		onToggle,
		onSearch
	}: {
		id: string;
		label: string;
		placeholder: string;
		options: PrincipalOption[];
		value: string[];
		notes?: Record<string, string>;
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		searching?: boolean;
		searchError?: string;
		onToggle: (key: string) => void;
		onSearch?: (query: string) => void;
	} = $props();

	let open = $state(false);
	let query = $state('');
	let byKey = $derived(new Map(options.map((option) => [option.key, option])));
	let selected = $derived(
		value.map(
			(key): PrincipalOption =>
				byKey.get(key) ?? {
					key,
					label: key.replace(/^(u|g|ad):/, ''),
					kind: key.startsWith('g:') ? 'group' : 'user',
					directory: key.startsWith('ad:')
				}
		)
	);
	let summary = $derived.by(() => {
		const names = selected.map((option) => `${option.kind === 'group' ? '@' : ''}${option.label}`);
		return names.length > 2
			? `${names.slice(0, 2).join(', ')}, +${names.length - 2} more`
			: names.join(', ');
	});
	let warned = $derived(selected.filter((option) => option.warning));
	let warnedText = $derived(
		warned.length === 1
			? `${warned[0].label} has no Samba password, so they can’t sign in`
			: `${warned.length} users have no Samba password, so they can’t sign in`
	);
	let users = $derived(options.filter((option) => option.kind === 'user'));
	let groups = $derived(options.filter((option) => option.kind === 'group'));

	watch(
		() => query,
		(current) => onSearch?.(current),
		{ lazy: true }
	);

	function icon(option: PrincipalOption): string {
		if (option.kind === 'group')
			return option.directory ? 'icon-[mdi--account-group-outline]' : 'icon-[lucide--users]';
		return option.directory ? 'icon-[mdi--account-outline]' : 'icon-[lucide--user]';
	}
</script>

{#snippet item(option: PrincipalOption)}
	{@const chosen = value.includes(option.key)}
	<Command.Item value="{option.label} {option.key}" onSelect={() => onToggle(option.key)}>
		<span class="{icon(option)} text-muted-foreground size-4 shrink-0" aria-hidden="true"></span>
		<span class="min-w-0 flex-1">
			<span class="block truncate">{option.kind === 'group' ? '@' : ''}{option.label}</span>
			{#if option.warning || notes[option.key] || option.directory}
				<span
					class="block truncate text-xs {option.warning
						? 'text-amber-600 dark:text-amber-400'
						: 'text-muted-foreground'}"
				>
					{option.warning ?? notes[option.key] ?? 'Active Directory'}
				</span>
			{/if}
		</span>
		{#if chosen}
			<span class="icon-[lucide--check] size-4 shrink-0" aria-hidden="true"></span>
			<span class="sr-only">(selected)</span>
		{/if}
	</Command.Item>
{/snippet}

<div class="min-w-0 space-y-2">
	<div class="flex min-h-5 items-baseline">
		<Label for={id}>{label}</Label>
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
					aria-invalid={invalid || undefined}
					aria-describedby={describedBy}
					class="bg-background aria-invalid:border-destructive dark:aria-invalid:border-destructive w-full justify-between px-3 font-normal dark:hover:bg-input/30"
				>
					<span class="truncate {selected.length ? '' : 'text-muted-foreground'}">
						{selected.length ? summary : placeholder}
					</span>
					{#if warned.length}
						<span
							class="icon-[lucide--triangle-alert] ml-auto size-4 shrink-0 text-amber-600 dark:text-amber-400"
							title={warnedText}
							aria-hidden="true"
						></span>
						<span class="sr-only">({warnedText})</span>
					{/if}
					<span
						class="icon-[lucide--chevrons-up-down] size-4 shrink-0 opacity-50 {warned.length
							? ''
							: 'ml-auto'}"
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
				<Command.Input
					bind:value={query}
					placeholder={onSearch
						? 'Search users, groups or the directory…'
						: 'Search users and groups…'}
				/>
				<Command.List class="max-h-72">
					<Command.Empty>
						{searching ? 'Searching…' : searchError || 'No matching users or groups.'}
					</Command.Empty>
					{#if users.length}
						<Command.Group heading="Users">
							{#each users as option (option.key)}{@render item(option)}{/each}
						</Command.Group>
					{/if}
					{#if groups.length}
						<Command.Group heading="Groups">
							{#each groups as option (option.key)}{@render item(option)}{/each}
						</Command.Group>
					{/if}
					{#if searching && (users.length || groups.length)}
						<p class="text-muted-foreground flex items-center gap-2 px-3 py-2 text-xs">
							<span class="icon-[lucide--loader-circle] size-3.5 animate-spin" aria-hidden="true"
							></span>Searching the directory…
						</p>
					{:else if searchError && (users.length || groups.length)}
						<p class="text-muted-foreground px-3 py-2 text-xs">{searchError}</p>
					{/if}
				</Command.List>
			</Command.Root>
		</Popover.Content>
	</Popover.Root>
</div>
