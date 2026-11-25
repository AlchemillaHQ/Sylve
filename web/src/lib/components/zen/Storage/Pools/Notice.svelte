<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { statusTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import type { ZpoolStatusPool } from '$lib/types/zfs/pool';
	import { untrack } from 'svelte';
	import { reportedNumber } from './pools';

	let {
		status,
		poolState,
		errors
	}: { status: ZpoolStatusPool; poolState: string; errors: number } = $props();

	const appearance = {
		info: {
			accent: 'text-zen-highlight-foreground',
			icon: 'icon-[lucide--info]'
		},
		warning: {
			accent: 'text-amber-700 dark:text-amber-400',
			icon: 'icon-[lucide--triangle-alert]'
		},
		danger: {
			accent: 'text-red-700 dark:text-red-400',
			icon: 'icon-[lucide--circle-alert]'
		}
	};

	let notice = $derived.by(() => {
		const health = statusTone(
			poolState,
			errors + (reportedNumber(status.scan_stats?.errors) ?? 0),
			0
		);
		const message = status.status?.replace(/\s+/g, ' ').trim() ?? '';
		const corruption = /\b(?:data corruption|corrupt(?:ed)? (?:data|metadata))\b/i.test(message);
		if (health === 'danger' || corruption) {
			return {
				tone: 'danger' as const,
				title: corruption ? 'ZFS reports data corruption' : 'ZFS reports a pool problem',
				description: 'Review the diagnostic and recommended action below.'
			};
		}
		const features =
			/^Some supported (?:and requested )?features are not enabled on the pool\. The pool can still be used, but some features are unavailable\.$/i.test(
				message
			);
		if (health === 'success' && features) {
			return {
				tone: 'info' as const,
				title: 'Additional ZFS features available',
				description: 'The pool remains usable, but some supported features aren’t enabled.'
			};
		}
		return {
			tone: 'warning' as const,
			title: health === 'warning' ? 'Pool health needs attention' : 'ZFS recommends attention',
			description: 'Review the diagnostic and recommended action below.'
		};
	});
	let tone = $derived(notice.tone);
	let expanded = $state(untrack(() => tone !== 'info'));
	let style = $derived(appearance[tone]);

	$effect(() => {
		if (tone !== 'info') expanded = true;
	});
</script>

<section
	class="bg-card border-border flex min-w-0 gap-2 rounded-lg border px-6 pt-5 pb-3 text-sm"
	role={notice.tone === 'danger' ? 'alert' : 'status'}
>
	<span
		class="bg-muted mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md {style.accent}"
		aria-hidden="true"
	>
		<span class="size-5 {style.icon}"></span>
	</span>
	<div class="min-w-0 flex-1">
		<h2 class="text-foreground text-base leading-6 font-bold">{notice.title}</h2>
		<p class="text-muted-foreground mt-1 text-[13px] leading-5">{notice.description}</p>
		{#if notice.tone === 'info'}
			<p class="text-muted-foreground mt-1 text-[13px] leading-5">
				Upgrading may prevent access from older ZFS versions.
			</p>
		{/if}
		<details class="group mt-2 text-xs" bind:open={expanded}>
			<summary
				class="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
			>
				<span
					class="icon-[lucide--chevron-right] group-open:icon-[lucide--chevron-down] size-3.5 group-open:size-3.5 shrink-0"
					aria-hidden="true"
				></span>Original ZFS message
			</summary>
			<dl class="mt-3 space-y-4 border-t pt-3 text-[13px] leading-5">
				{#if status.status}
					<div>
						<dt class="text-foreground flex items-center gap-2 font-semibold">
							<span
								class="icon-[lucide--file-text] text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>Status
						</dt>
						<dd
							class="text-muted-foreground mt-1.5 pl-6 whitespace-normal [overflow-wrap:anywhere]"
						>
							{status.status}
						</dd>
					</div>
				{/if}
				{#if status.action}
					<div>
						<dt class="text-foreground flex items-center gap-2 font-semibold">
							<span
								class="icon-[lucide--list-checks] text-muted-foreground size-4 shrink-0"
								aria-hidden="true"
							></span>Recommended action
						</dt>
						<dd
							class="text-muted-foreground mt-1.5 pl-6 whitespace-normal [overflow-wrap:anywhere]"
						>
							{status.action}
						</dd>
					</div>
				{/if}
			</dl>
		</details>
	</div>
</section>
