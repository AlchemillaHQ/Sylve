<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	let { limited = false }: { limited?: boolean } = $props();
</script>

<div class="px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"
			>
				<span class="icon-[carbon--assembly-cluster] size-5"></span>
			</span>
			<h1 class="text-2xl font-semibold tracking-tight">Datacenter</h1>
		</div>
		<span class="text-muted-foreground rounded-md border px-2.5 py-1 text-xs"
			>Navigation Preview</span
		>
	</header>

	<div class="bg-card mb-5 rounded-lg border p-4">
		<h2 class="text-sm font-medium">Global management, separate from your nodes</h2>
		<p class="text-muted-foreground mt-1.5 max-w-3xl text-sm leading-relaxed">
			This is a rough navigation skeleton, not a live datacenter dashboard. Use the scope selector
			above to switch between Datacenter and a node. The sidebar changes with the selected scope;
			unfinished destinations are placeholders.
		</p>
		{#if limited}
			<p class="text-muted-foreground mt-3 text-sm">
				Your permissions do not include the live global overview. Only permitted navigation items
				are shown.
			</p>
		{/if}
	</div>

	<div class="grid gap-4 md:grid-cols-3">
		<section class="bg-card rounded-lg border p-4">
			<h2 class="flex items-center gap-2 text-sm font-semibold">
				<span
					class="icon-[fluent--storage-20-filled] text-muted-foreground size-4"
					aria-hidden="true"
				></span>Nodes
			</h2>
			<p class="text-muted-foreground mt-2 text-sm">
				A global view of node health and capacity will live here.
			</p>
			<div class="mt-5 space-y-3" aria-hidden="true">
				{#each [1, 2, 3] as row (row)}
					<div class="flex items-center gap-3 rounded-md border p-3">
						<div class="bg-muted size-7 rounded-md"></div>
						<div class="flex-1 space-y-2">
							<div class="bg-muted h-2 w-1/2 rounded"></div>
							<div class="bg-muted h-2 w-3/4 rounded"></div>
						</div>
					</div>
				{/each}
			</div>
			<p class="text-muted-foreground mt-4 text-xs">Layout placeholder · No live metrics</p>
		</section>
		<section class="bg-card rounded-lg border p-4">
			<h2 class="flex items-center gap-2 text-sm font-semibold">
				<span class="icon-[mdi--backup-restore] text-muted-foreground size-4" aria-hidden="true"
				></span>Data Protection
			</h2>
			<p class="text-muted-foreground mt-2 text-sm">
				Manage backup and replication policies across nodes.
			</p>
			<ul class="text-muted-foreground mt-5 divide-y rounded-md border text-sm">
				<li class="px-3 py-3">Backup Targets</li>
				<li class="px-3 py-3">Backup Jobs & Events</li>
				<li class="px-3 py-3">Replication Policies & Events</li>
			</ul>
			<p class="text-muted-foreground mt-4 text-xs">Navigation only · Not implemented yet</p>
		</section>
		<section class="bg-card rounded-lg border p-4">
			<h2 class="flex items-center gap-2 text-sm font-semibold">
				<span class="icon-[mdi--shield-account] text-muted-foreground size-4" aria-hidden="true"
				></span>Access
			</h2>
			<p class="text-muted-foreground mt-2 text-sm">
				Sylve access belongs here. Unix accounts stay under a node's Host Access section.
			</p>
			<ul class="text-muted-foreground mt-5 divide-y rounded-md border text-sm">
				<li class="px-3 py-3">Users & Groups</li>
				<li class="px-3 py-3">Roles & Grants</li>
				<li class="px-3 py-3">Login Realms</li>
			</ul>
			<p class="text-muted-foreground mt-4 text-xs">Navigation only · Not implemented yet</p>
		</section>
	</div>
</div>
