<script lang="ts">
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';

	interface Props {
		label?: string;
		placeholder?: string;
		options: Array<{ value: string; label: string }>;
		value: string;
		classes?: { parent?: string; label?: string };
		onChange: (value: string) => void;
		disabled?: boolean;
		single?: boolean;
	}

	let {
		label,
		placeholder = 'Select an option',
		options,
		classes = { parent: 'flex-1 min-w-0 space-y-1', label: 'w-24 whitespace-nowrap text-sm' },
		value = $bindable(),
		onChange,
		disabled = false
	}: Props = $props();

	let sLabel = $derived(value ? options.find((o) => o.value === value)?.label : placeholder || undefined);
</script>

<div class={`${classes.parent} min-w-0`}>
	{#if label}
		<Label class={classes.label}>{label}</Label>
	{/if}

	<Select.Root
		type="single"
		bind:value
		onValueChange={() => onChange(value)}
		{disabled}
	>
		<Select.Trigger
			class="w-full max-w-full min-w-0 h-9 px-3 inline-flex items-center overflow-hidden text-left"
			title={sLabel || placeholder}
		>
			<span class="block truncate">{sLabel || placeholder}</span>
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
