<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { toast } from 'svelte-sonner';

	let { name, addresses }: { name: string; addresses: { ip: string; family: string }[] } = $props();

	async function copyAddress(ip: string) {
		try {
			await navigator.clipboard.writeText(ip);
			toast.success('IP address copied to clipboard', {
				duration: 2000,
				position: 'bottom-center'
			});
		} catch {
			toast.error('Failed to copy IP address', { duration: 2000, position: 'bottom-center' });
		}
	}
</script>

<Popover.Root>
	<Popover.Trigger
		class="hover:bg-muted focus-visible:outline-ring inline-flex max-w-full min-w-0 items-baseline gap-1 rounded-sm font-mono text-xs leading-4 tabular-nums focus-visible:outline-2 focus-visible:outline-offset-2"
		aria-label="Show IP addresses for {name}"
	>
		<span class="truncate">{addresses[0].ip}</span>
		{#if addresses.length > 1}
			<span class="text-muted-foreground shrink-0">+{addresses.length - 1}</span>
		{/if}
	</Popover.Trigger>
	<Popover.Content align="start" class="w-80 max-w-[calc(100vw-2rem)] p-3">
		<h3 class="text-sm font-semibold">
			IP Addresses <span class="text-muted-foreground font-normal">· {name}</span>
		</h3>
		<ul class="mt-2 max-h-64 divide-y overflow-y-auto overscroll-contain">
			{#each addresses as address (address.ip)}
				<li class="flex items-center gap-3 py-2 first:pt-0 last:pb-0">
					<div class="min-w-0 flex-1">
						<p class="text-muted-foreground text-[11px]">{address.family}</p>
						<p class="mt-0.5 font-mono text-xs break-all select-text">{address.ip}</p>
					</div>
					<Button
						variant="ghost"
						size="icon"
						class="size-7 shrink-0"
						aria-label="Copy {address.ip}"
						onclick={() => copyAddress(address.ip)}
					>
						<span class="icon-[lucide--copy] size-3.5" aria-hidden="true"></span>
					</Button>
				</li>
			{/each}
		</ul>
	</Popover.Content>
</Popover.Root>
