<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import { isAPIResponse } from '$lib/utils/http';
	import { resource } from 'runed';

	let { node, datacenter = false }: { node: string; datacenter?: boolean } = $props();

	const nodes = resource(
		() => 'zen-nodes',
		async () => {
			const details = await getDetails();
			if (isAPIResponse(details) || !details.cluster.enabled) return [];
			const result = await getNodesResult();
			return isAPIResponse(result) ? [] : result;
		},
		{ initialValue: [] as ClusterNode[] }
	);

	let clustered = $derived(nodes.current.length > 0);
	let entries = $derived(
		clustered
			? nodes.current.map((item) => ({ hostname: item.hostname, online: item.status === 'online' }))
			: node
				? [{ hostname: node, online: true }]
				: []
	);
</script>

<DropdownMenu.Root onOpenChange={(open) => open && nodes.refetch()}>
	<DropdownMenu.Trigger>
		{#snippet child({ props })}
			<Button
				variant="ghost"
				class="h-10 min-w-0 shrink gap-2.5 px-2 text-[13px]"
				aria-label="Select scope"
				{...props}
			>
				<span
					class="text-muted-foreground size-4 shrink-0 {datacenter
						? 'icon-[carbon--assembly-cluster]'
						: 'icon-[lucide--server]'}"
					aria-hidden="true"
				></span>
				<span class="grid min-w-0 max-w-48 gap-0.5 text-left leading-tight">
					<span class="truncate font-medium">{datacenter ? 'Datacenter' : node}</span>
					<span class="text-muted-foreground text-[11px] font-normal">
						{#if datacenter}Global Scope{:else if clustered}Node · Cluster{:else}Node · Standalone{/if}
					</span>
				</span>
				<span class="icon-[lucide--chevron-down] text-muted-foreground size-3.5 shrink-0"></span>
			</Button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content class="min-w-60" align="start">
		<DropdownMenu.Label class="text-muted-foreground text-xs font-normal">Scope</DropdownMenu.Label>
		<DropdownMenu.Item class="cursor-pointer" onSelect={() => useSafeGoto('/datacenter/summary')}>
			<span class="icon-[carbon--assembly-cluster] size-4" aria-hidden="true"></span>
			<span>Datacenter</span>
			{#if datacenter}<span class="icon-[lucide--check] ml-auto size-4" aria-hidden="true"
				></span>{/if}
		</DropdownMenu.Item>
		<DropdownMenu.Separator />
		<DropdownMenu.Label class="text-muted-foreground text-xs font-normal">Nodes</DropdownMenu.Label>
		{#each entries as entry (entry.hostname)}
			<DropdownMenu.Item
				class="cursor-pointer"
				disabled={!entry.online}
				onSelect={() => useSafeGoto(`/${entry.hostname}/summary`)}
			>
				<span class="icon-[lucide--server] size-4"></span>
				<span class="truncate">{entry.hostname}</span>
				{#if !entry.online}
					<span class="text-muted-foreground ml-auto text-xs">Offline</span>
				{:else if !datacenter && entry.hostname === node}
					<span class="icon-[lucide--check] ml-auto size-4"></span>
				{/if}
			</DropdownMenu.Item>
		{/each}
		{#if !clustered}
			<DropdownMenu.Separator />
			<p class="text-muted-foreground px-2 py-1.5 text-xs">This node is not part of a cluster.</p>
		{/if}
	</DropdownMenu.Content>
</DropdownMenu.Root>
