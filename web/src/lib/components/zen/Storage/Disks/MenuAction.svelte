<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';

	let {
		label,
		icon,
		blocked = '',
		destructive = false,
		onSelect
	}: {
		label: string;
		icon: string;
		blocked?: string;
		destructive?: boolean;
		onSelect: () => void;
	} = $props();
	let variant = $derived<'default' | 'destructive'>(destructive ? 'destructive' : 'default');
	let tone = $derived(
		destructive
			? 'dark:[--destructive:var(--color-red-300)] dark:data-[variant=destructive]:data-highlighted:bg-destructive/10'
			: ''
	);
</script>

{#if blocked}
	<Tooltip.Root delayDuration={500}>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<DropdownMenu.Item
					{...props}
					{variant}
					class="data-[disabled]:pointer-events-auto data-[disabled]:cursor-help {tone}"
					disabled
				>
					<span class="{icon} size-4" aria-hidden="true"></span>{label}
					<span class="sr-only">: {blocked}</span>
					<span class="icon-[lucide--info] ml-auto size-3.5" aria-hidden="true"></span>
				</DropdownMenu.Item>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content side="left" sideOffset={8} class="z-60 max-w-64 text-left text-pretty">
			{blocked}
		</Tooltip.Content>
	</Tooltip.Root>
{:else}
	<DropdownMenu.Item {variant} class="cursor-pointer {tone}" {onSelect}>
		<span class="{icon} size-4" aria-hidden="true"></span>{label}
	</DropdownMenu.Item>
{/if}
