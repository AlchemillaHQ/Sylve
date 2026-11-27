<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { clientProblem, splitClients } from './nfs';

	let {
		id,
		value,
		disabled = false,
		invalid = false,
		describedBy,
		onChange,
		onBlur
	}: {
		id: string;
		value: string[];
		disabled?: boolean;
		invalid?: boolean;
		describedBy?: string;
		onChange: (next: string[]) => void;
		onBlur?: () => void;
	} = $props();

	let text = $state('');
	let field = $state<HTMLInputElement>();

	function commit() {
		const added = splitClients(text).filter((entry) => !value.includes(entry));
		text = '';
		if (added.length) onChange([...value, ...new Set(added)]);
	}

	function remove(entry: string) {
		onChange(value.filter((item) => item !== entry));
		field?.focus();
	}

	function keydown(event: KeyboardEvent) {
		if (event.key === 'Enter' || event.key === ',' || event.key === ' ') {
			event.preventDefault();
			if (text.trim()) commit();
		} else if (event.key === 'Backspace' && !text && value.length) {
			onChange(value.slice(0, -1));
		}
	}

	function paste(event: ClipboardEvent & { currentTarget: HTMLInputElement }) {
		const pasted = event.clipboardData?.getData('text') ?? '';
		if (!/[\s,]/.test(pasted)) return;
		event.preventDefault();
		const input = event.currentTarget;
		const start = input.selectionStart ?? text.length;
		const end = input.selectionEnd ?? text.length;
		text = text.slice(0, start) + pasted + text.slice(end);
		commit();
	}
</script>

<div
	class="border-input dark:bg-input/30 focus-within:border-ring focus-within:ring-ring/50 flex min-h-9 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md border bg-transparent px-2 py-1.5 shadow-xs focus-within:ring-[3px] {invalid
		? 'border-destructive dark:border-destructive'
		: ''} {disabled ? 'cursor-not-allowed opacity-50' : ''}"
	role="presentation"
	onclick={() => field?.focus()}
>
	{#if value.length}
		<ul class="flex max-w-full min-w-0 flex-wrap gap-1.5" aria-label="Addresses and networks">
			{#each value as entry (entry)}
				{@const problem = clientProblem(entry)}
				<li
					class="flex max-w-full min-w-0 items-center gap-1 rounded border py-0.5 pr-0.5 pl-1.5 font-mono text-xs {problem
						? 'border-destructive text-destructive'
						: 'bg-muted'}"
				>
					{#if problem}
						<Tooltip.Root delayDuration={500}>
							<Tooltip.Trigger
								type="button"
								class="focus-visible:ring-ring min-w-0 cursor-help truncate rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
							>
								{entry}<span class="sr-only">: {problem}</span>
							</Tooltip.Trigger>
							<Tooltip.Content side="top" sideOffset={6} class="z-60 max-w-64 text-left">
								{problem}
							</Tooltip.Content>
						</Tooltip.Root>
					{:else}
						<span class="min-w-0 truncate">{entry}</span>
					{/if}
					<button
						type="button"
						class="hover:bg-background focus-visible:ring-ring flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none disabled:cursor-not-allowed"
						aria-label="Remove {entry}"
						{disabled}
						onclick={(event) => {
							event.stopPropagation();
							remove(entry);
						}}
					>
						<span class="icon-[lucide--x] size-3.5" aria-hidden="true"></span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
	<input
		bind:this={field}
		{id}
		bind:value={text}
		class="placeholder:text-muted-foreground min-w-40 flex-1 bg-transparent px-1 font-mono text-sm outline-none disabled:cursor-not-allowed"
		placeholder={value.length ? 'Add Another Address' : '192.168.1.0/24'}
		autocomplete="off"
		autocapitalize="off"
		spellcheck={false}
		{disabled}
		aria-invalid={invalid || undefined}
		aria-describedby={describedBy}
		onkeydown={keydown}
		onpaste={paste}
		onblur={() => {
			commit();
			onBlur?.();
		}}
	/>
</div>
