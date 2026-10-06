<!-- SPDX-License-Identifier: BSD-2-Clause -->

<script lang="ts">
	import { getISCSIConfig } from '$lib/api/iscsi/config';
	import Config from '$lib/components/custom/iSCSI/Config.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { ISCSIConfig } from '$lib/types/iscsi/config';
	import { isAPIResponse } from '$lib/utils/http';
	import { onMount } from 'svelte';

	let { open = $bindable(), node }: { open: boolean; node: string } = $props();
	let config = $state<ISCSIConfig | APIResponse | null>(null);

	async function load() {
		config = null;
		config = await getISCSIConfig({ hostname: node });
	}

	onMount(() => {
		void load();
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[90vh] flex-col overflow-hidden sm:max-w-200"
		onClose={() => (open = false)}
	>
		<Dialog.Header class="shrink-0 pr-6">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--cog-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Target Settings"
				/>
			</Dialog.Title>
		</Dialog.Header>
		<div class="min-h-0 overflow-y-auto pr-1">
			{#if config === null}
				<div
					class="flex min-h-72 items-center justify-center gap-2 text-sm text-muted-foreground"
					role="status"
				>
					<span
						class="icon-[mdi--loading] size-5 animate-spin motion-reduce:animate-none"
						aria-hidden="true"
					></span>
					<span>Loading...</span>
				</div>
			{:else if isAPIResponse(config)}
				<div class="space-y-3">
					<p
						class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500"
						role="alert"
					>
						Cannot read the target configuration.
					</p>
					<Button variant="outline" size="sm" onclick={load}>Retry</Button>
				</div>
			{:else}
				<Config {node} {config} />
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
