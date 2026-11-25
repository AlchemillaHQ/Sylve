<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import KvTableModal from '$lib/components/custom/KVTableModal.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { Iface } from '$lib/types/network/iface';
	import { getCleanIfaceData } from '$lib/utils/network/iface';
	import { linkSpeed } from './format';
	import InterfaceAddresses from './InterfaceAddresses.svelte';
	import Panel from './Panel.svelte';

	let { interfaces, class: className = '' }: { interfaces: Iface[]; class?: string } = $props();
	let selected = $state<Iface | null>(null);
	let detailsOpen = $state(false);

	function viewInterface(iface: Iface) {
		selected = iface;
		detailsOpen = true;
	}

	let rows = $derived(
		interfaces
			.filter((iface) => !iface.groups?.includes('lo'))
			.map((iface) => {
				const addresses = [
					...(iface.ipv4 ?? []).map((address) => ({ ip: address.ip, family: 'IPv4' })),
					...(iface.ipv6 ?? []).map((address) => ({ ip: address.ip, family: 'IPv6' }))
				].sort(
					(left, right) =>
						Number(left.ip.toLowerCase().startsWith('fe80:')) -
						Number(right.ip.toLowerCase().startsWith('fe80:'))
				);
				const connected = iface.media
					? iface.media.status === 'active'
					: (iface.flags.desc ?? []).includes('UP');
				return {
					iface,
					addresses,
					connected,
					speed: linkSpeed(iface.media?.subtype ?? '')
				};
			})
			.sort(
				(left, right) =>
					Number(right.connected) - Number(left.connected) ||
					Number(right.addresses.length > 0) - Number(left.addresses.length > 0)
			)
	);

	let connectedCount = $derived(rows.filter((row) => row.connected).length);
</script>

<Panel
	title="Network Interfaces"
	icon="icon-[lucide--ethernet-port]"
	class="@container {className}"
>
	{#snippet aside()}
		{connectedCount} Connected
	{/snippet}

	<ScrollArea
		fade
		fadeClasses="from-card"
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'max-h-60 overscroll-contain @xl:max-h-28',
			role: 'region',
			'aria-label': 'Network Interfaces',
			tabindex: 0
		}}
	>
		{#if rows.length === 0}
			<p class="text-muted-foreground text-sm">No network interfaces.</p>
		{:else}
			<ul class="grid gap-x-6 gap-y-3 text-[13px] @xl:grid-cols-2">
				{#each rows as row (row.iface.name)}
					<li class="min-h-12 min-w-0">
						<button
							type="button"
							class="hover:text-zen-accent focus-visible:outline-ring flex w-full min-w-0 items-center gap-2 rounded-sm text-left focus-visible:outline-2 focus-visible:outline-offset-2"
							aria-label="View details for {row.iface.name}"
							onclick={() => viewInterface(row.iface)}
						>
							<span class="flex size-4 shrink-0 items-center justify-center" aria-hidden="true">
								<span
									class="size-1.5 rounded-full {row.connected
										? 'bg-green-600 dark:bg-green-500'
										: 'bg-muted-foreground/50'}"
								></span>
							</span>
							<span class="truncate font-medium">
								{row.iface.description || row.iface.name}
							</span>
							{#if !row.connected}
								<span class="text-muted-foreground shrink-0 text-xs">Disconnected</span>
							{:else if row.speed}
								<span class="text-muted-foreground shrink-0 text-xs">· {row.speed}</span>
							{/if}
							<span
								class="icon-[lucide--chevron-right] text-muted-foreground ml-auto size-3 shrink-0"
								aria-hidden="true"
							></span>
						</button>
						<div class="mt-1 flex min-w-0 flex-wrap items-baseline gap-x-1.5 gap-y-1 pl-6 text-xs">
							{#if row.iface.description}
								<span class="text-muted-foreground break-all">{row.iface.name}</span>
								<span class="text-muted-foreground" aria-hidden="true">·</span>
							{/if}
							{#if row.addresses.length > 0}
								<InterfaceAddresses name={row.iface.name} addresses={row.addresses} />
							{:else}
								<span class="text-muted-foreground">No IP addresses</span>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</ScrollArea>
</Panel>

{#if selected}
	{#key selected.name}
		<KvTableModal
			bind:open={detailsOpen}
			titles={{
				icon: 'lucide--ethernet-port',
				main: `Details - ${selected.name}`,
				key: 'Attribute',
				value: 'Value'
			}}
			KV={{
				...getCleanIfaceData(selected),
				MTU: selected.mtu ?? '-',
				Metric: selected.metric ?? '-'
			}}
		/>
	{/key}
{/if}
