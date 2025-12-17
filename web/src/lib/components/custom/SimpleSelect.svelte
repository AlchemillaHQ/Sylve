<script lang="ts">
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';

	interface Props {
		label?: string;
		icon?: string;
		placeholder?: string;
		options: Array<{ value: string; label: string }>;
		value: string;
		classes?: { parent?: string; label?: string; trigger?: string };
		onChange: (value: string) => void;
		title?: string;
		disabled?: boolean;
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
		title
	}: Props = $props();

	let sLabel = $derived(
		value ? options.find((o) => o.value === value)?.label : placeholder || undefined
	);
</script>

<div class={`${classes.parent} min-w-0`}>
	{#if label}
		<Label class={classes.label}>
			{label}
		</Label>
	{/if}

	<Select.Root type="single" bind:value onValueChange={() => onChange(value)} {disabled}>
		<Select.Trigger class={classes.trigger} title={title || sLabel || placeholder}>
			{#if icon}
				<span class={icon + ' mt-0.5 h-4 w-4'}></span>
			{/if}
			<span class="block truncate">
				{sLabel || placeholder}
			</span>
		</Select.Trigger>

		<Select.Content>
			{#each options as option (option.value)}
				<Select.Item value={option.value} label={option.label} title={option.label}>
					{option.label}
				</Select.Item>
			{/each}
		</Select.Content>
	</Select.Root>
</div>
