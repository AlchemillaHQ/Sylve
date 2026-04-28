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
		classes?: string;
		inputClasses?: string;
		type?: string;
		hint?: string;
		textAreaClasses?: string;
		disabled?: boolean;
		revealOnFocus?: boolean;
		onChange?: (value: string | number) => void;
		onBlur?: () => void;
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
		hint = '',
		placeholder = '',
		autocomplete = 'off',
		classes = 'space-y-1.5',
		inputClasses = '',
		type = 'text',
		textAreaClasses = 'min-h-56',
		topRightButton,
		disabled = false,
		revealOnFocus = false,
		onChange,
		onBlur
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let nanoId = $state(generateNanoId(label));
	let passwordFocused = $state(false);
	let effectiveType = $derived(
		type === 'password' && revealOnFocus && passwordFocused ? 'text' : type
	);
</script>

<div class={`${classes}`}>
	{#if label}
		{#if hint || topRightButton}
			<div class="flex h-7 items-center justify-between w-full">
				<Label class="whitespace-nowrap text-sm" for={nanoId}>
					{#if labelHTML}
						<!-- eslint-disable-next-line svelte/no-at-html-tags-->
						{@html label}
					{:else}
						{label}
					{/if}
				</Label>

				{#if hint}
					<Tooltip.Root>
						<Tooltip.Trigger
							aria-label="Help Information"
							class="inline-flex items-center justify-center"
						>
							<span class="icon icon-[mdi--help-circle-outline] size-4"></span>
						</Tooltip.Trigger>

						<Tooltip.Content
							class="w-fit max-w-62.5 min-w-0 text-balance wrap-break-word whitespace-normal"
						>
							{hint}
						</Tooltip.Content>
					</Tooltip.Root>
				{/if}

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
		{:else}
			<Label class="whitespace-nowrap text-sm" for={nanoId}>
				{#if labelHTML}
					<!-- eslint-disable-next-line svelte/no-at-html-tags-->
					{@html label}
				{:else}
					{label}
				{/if}
			</Label>
		{/if}
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
				value = (e.currentTarget as HTMLTextAreaElement).value;
				if (onChange) onChange(value);
			}}
			onblur={() => {
				if (onBlur) onBlur();
			}}
		/>
	{:else}
		<Input
			{...{ type: effectiveType }}
			id={nanoId}
			{placeholder}
			{autocomplete}
			class={inputClasses || undefined}
			onfocus={() => {
				if (type === 'password' && revealOnFocus) passwordFocused = true;
			}}
			bind:value
			{disabled}
			oninput={(e) => {
				value = (e.currentTarget as HTMLInputElement).value;
				if (onChange) onChange(value);
			}}
			onblur={() => {
				if (type === 'password' && revealOnFocus) passwordFocused = false;
				if (onBlur) onBlur();
			}}
		/>
	{/if}
</div>
