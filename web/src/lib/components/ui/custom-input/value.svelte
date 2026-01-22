<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import Textarea from '$lib/components/ui/textarea/textarea.svelte';
	import { generateNanoId } from '$lib/utils/string';
	import type { FullAutoFill } from 'svelte/elements';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';

	interface Props {
		label?: string;
		labelHTML?: boolean;
		value: string | number;
		placeholder: string;
		autocomplete?: FullAutoFill | null | undefined;
		classes: string;
		type?: string;
		textAreaClasses?: string;
		disabled?: boolean;
		onChange?: (value: string | number) => void;
		topRightButton?: {
			icon: string;
			tooltip: string;
			function: () => Promise<string>;
		};
	}

	let {
		value = $bindable(''),
		label = '',
		labelHTML = false,
		placeholder = '',
		autocomplete = 'off',
		classes = 'space-y-1.5',
		type = 'text',
		textAreaClasses = 'min-h-56',
		topRightButton,
		disabled = false,
		onChange
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let nanoId = $state(generateNanoId(label));
</script>

<div class={`${classes}`}>
	{#if label}
		<div class="flex items-center justify-between w-full">
			<Label class="whitespace-nowrap text-sm" for={nanoId}>
				{#if labelHTML}
					{@html label}
				{:else}
					{label}
				{/if}
			</Label>

			{#if topRightButton}
				<Button
					variant="outline"
					size="icon"
					class="h-7 w-7 shrink-0"
					title={topRightButton.tooltip}
					onclick={async () => {
						const result = await topRightButton.function();
						if (result) value = result;
					}}
				>
					<span class={`icon ${topRightButton.icon} size-4`}></span>
				</Button>
			{/if}
		</div>
	{/if}

	{#if type === 'textarea'}
		<Textarea
			class={textAreaClasses}
			id={nanoId}
			{placeholder}
			{autocomplete}
			bind:value
			{disabled}
			oninput={(e) => {
				value = e.target?.value;
				if (onChange) onChange(value);
			}}
		/>
	{:else}
		<Input
			{type}
			id={nanoId}
			{placeholder}
			{autocomplete}
			bind:value
			{disabled}
			oninput={(e) => {
				value = e.target?.value;
				if (onChange) onChange(value);
			}}
		/>
	{/if}
</div>
