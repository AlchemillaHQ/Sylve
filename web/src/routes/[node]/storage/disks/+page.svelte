<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import ClassicDisks from '$lib/components/custom/Disk/Disks.svelte';
	import Disks from '$lib/components/zen/Storage/Disks/Disks.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#if data.denied || !allowed('Sys.Audit', nodePath(data.hostname))}
		<div
			class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-6 text-center"
		>
			<span class="icon-[mdi--account-lock-open-outline] size-8" aria-hidden="true"></span>
			<p class="text-foreground font-medium">Storage Disks Requires Host Access</p>
			<p class="text-sm">You do not have permission to read disks on this node.</p>
		</div>
	{:else}
		{#key data.hostname}
			<Disks {data} />
		{/key}
	{/if}
{:else}
	<ClassicDisks data={{ disks: Array.isArray(data.disks) ? data.disks : [] }} />
{/if}
