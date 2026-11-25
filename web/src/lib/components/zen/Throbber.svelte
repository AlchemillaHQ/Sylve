<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SylveIcon from '$lib/components/custom/SylveIcon.svelte';
	import { fade } from 'svelte/transition';
</script>

<div
	class="bg-background fixed inset-0 z-10001 flex flex-col items-center justify-center gap-6"
	transition:fade={{ duration: 300 }}
	role="status"
>
	<div class="flex size-16 shrink-0 items-center justify-center">
		<SylveIcon class="size-16" />
	</div>
	<!-- @wc-ignore -->
	<p class="pl-[.45em] text-lg font-normal tracking-[.45em]">SYLVE</p>
	<div class="bg-foreground/10 h-0.5 w-40 overflow-hidden rounded-full">
		<div class="glide bg-zen-accent h-full w-1/3 rounded-full"></div>
	</div>
	<span class="sr-only">Loading</span>
</div>

<style>
	.glide {
		animation: glide 1.6s ease-in-out infinite;
	}

	@keyframes glide {
		0% {
			transform: translateX(-100%);
		}

		100% {
			transform: translateX(300%);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.glide {
			animation: none;
			width: 100%;
		}
	}
</style>
