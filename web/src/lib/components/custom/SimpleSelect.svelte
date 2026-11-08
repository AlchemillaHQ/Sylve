<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';

	interface Props {
		label?: string;
		icon?: string;
		placeholder?: string;
		options: Array<{
			value: string;
			label: string;
			disabled?: boolean;
			icon?: string;
			detail?: string;
		}>;
		value: string;
		classes?: { parent?: string; label?: string; trigger?: string };
		onChange: (value: string) => void;
		title?: string;
		disabled?: boolean;
		size?: 'sm' | 'default';
		single?: boolean;
	}

	let {
		label,
		icon,
		placeholder = 'Select an option',
		options,
		classes = {
			parent: 'flex-1 min-w-0 space-y-1',
			label: 'w-24 whitespace-nowrap text-sm',
			trigger:
				'inline-flex h-8 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
		},
		value = $bindable(),
		onChange,
		disabled = false,
		size = 'default',
		title,
		single: _single
	}: Props = $props();

	let selected = $derived(options.find((o) => o.value === value));
	let sLabel = $derived(selected?.label ?? placeholder);
	let triggerIcon = $derived(selected?.icon || icon);
</script>

<div class={`${classes.parent} min-w-0`}>
	{#if label}
		<Label class={classes.label}>
			{label}
		</Label>
	{/if}

	<Select.Root type="single" bind:value onValueChange={() => onChange(value)} {disabled}>
		<Select.Trigger {size} class={classes.trigger} title={title || sLabel || placeholder}>
			<span class="flex min-w-0 items-center gap-2">
				{#if triggerIcon}
					<span class={triggerIcon + ' h-4 w-4 shrink-0'}></span>
				{/if}
				<span class="block truncate">
					{sLabel || placeholder}
				</span>
			</span>
		</Select.Trigger>

		<Select.Content>
			{#each options as option (option.value)}
				<Select.Item
					value={option.value}
					label={option.label}
					title={option.label}
					disabled={option.disabled}
				>
					{#if option.icon}
						<span class={option.icon + ' h-4 w-4 shrink-0'}></span>
					{/if}
					<span class="truncate">{option.label}</span>
					{#if option.detail}
						<span class="ml-auto shrink-0 pl-3 text-xs text-muted-foreground">{option.detail}</span>
					{/if}
				</Select.Item>
			{/each}
		</Select.Content>
	</Select.Root>
</div>
