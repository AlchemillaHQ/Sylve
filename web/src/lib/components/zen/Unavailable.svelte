<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import { homePath, setInterface } from '$lib/stores/interface.svelte';
</script>

<div class="flex h-full w-full flex-col items-center justify-center gap-4 p-6 text-center">
	<span class="icon-[lucide--construction] text-muted-foreground size-12"></span>
	<div class="max-w-md space-y-1">
		<p class="font-medium">This page isn't available in Zen Mode yet.</p>
		<p class="text-muted-foreground text-sm">You can still open it in the classic interface.</p>
	</div>
	<div class="flex flex-wrap items-center justify-center gap-2">
		<Button variant="outline" href={homePath()}>
			<span class="icon-[lucide--layout-dashboard] size-4"></span>
			Back to overview
		</Button>
		<Button onclick={() => setInterface('classic')}>
			<span class="icon-[lucide--arrow-left-right] size-4"></span>
			Open in classic interface
		</Button>
	</div>
</div>
