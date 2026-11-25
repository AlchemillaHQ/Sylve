<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { swatches } from '$lib/utils/terminal';
	import ColorPicker from 'svelte-awesome-color-picker';

	let {
		label,
		value = $bindable('#ffffff'),
		onChange
	}: { label: string; value: string; onChange: () => void } = $props();
	const id = $props.id();
</script>

<div class="space-y-2">
	<Label for={id}>{label}</Label>
	<Popover.Root>
		<Popover.Trigger {id}>
			{#snippet child({ props })}
				<Button {...props} variant="outline" class="h-9 w-full justify-start gap-2.5 px-3">
					<span
						class="size-4 shrink-0 rounded-sm border"
						style:background-color={value}
						aria-hidden="true"
					></span>
					<span class="font-mono text-xs">{value}</span>
				</Button>
			{/snippet}
		</Popover.Trigger>
		<Popover.Content
			align="start"
			sideOffset={8}
			collisionPadding={16}
			class="zen-terminal-color-popover bg-card z-60 max-h-(--bits-popover-content-available-height) w-auto max-w-[calc(100vw-2rem)] overflow-y-auto p-2"
			aria-label="{label} color"
		>
			<ColorPicker
				bind:hex={value}
				isDialog={false}
				isAlpha={false}
				{swatches}
				onInput={onChange}
			/>
		</Popover.Content>
	</Popover.Root>
</div>

<style>
	:global(.zen-terminal-color-popover .wrapper) {
		margin: 0;
		border: 0;
		padding: 0;
		background: transparent;
	}
</style>
