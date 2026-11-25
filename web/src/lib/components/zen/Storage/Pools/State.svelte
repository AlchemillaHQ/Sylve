<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { statusTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import { toneText } from '../dashboard';
	let {
		value,
		prominent = false,
		quiet = false,
		size = 'sm'
	}: { value: string; prominent?: boolean; quiet?: boolean; size?: 'sm' | 'md' } = $props();
	let state = $derived(value.trim() || 'UNKNOWN');
	let tone = $derived(
		['ONLINE', 'AVAIL'].includes(state.toUpperCase())
			? 'success'
			: state.toUpperCase() === 'INUSE'
				? 'neutral'
				: statusTone(state, 0, 0)
	);
	let quietSuccess = $derived(quiet && !prominent && tone === 'success');
</script>

<span
	class="inline-flex items-center {prominent
		? 'gap-2 text-[28px] leading-8 font-semibold tracking-tight'
		: size === 'md'
			? 'gap-1.5 text-[13px] leading-5 font-medium'
			: 'gap-1.5 text-xs'} {quietSuccess
		? 'text-muted-foreground dark:text-muted-foreground/85'
		: toneText[tone]}"
>
	<span
		class="{prominent
			? 'size-6'
			: quietSuccess && size === 'sm'
				? 'size-3.5'
				: 'size-4'} shrink-0 {quietSuccess
			? 'icon-[lucide--check] text-emerald-700/80 dark:text-emerald-400/70'
			: tone === 'success'
				? 'icon-[lucide--circle-check]'
				: tone === 'danger'
					? 'icon-[lucide--circle-x]'
					: tone === 'neutral'
						? 'icon-[lucide--info]'
						: 'icon-[lucide--triangle-alert]'}"
		aria-hidden="true"
	></span>
	<span class="capitalize">{state.toLowerCase()}</span>
</span>
