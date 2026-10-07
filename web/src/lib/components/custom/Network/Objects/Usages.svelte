<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		getNetworkObjectUsageLabels,
		networkObjectUsageTypes
	} from '$lib/utils/network/object-usage';

	interface Props {
		open: boolean;
		object: NetworkObject;
	}

	let { open = $bindable(), object }: Props = $props();
	const labels = $derived(getNetworkObjectUsageLabels(object));
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-xl" onClose={() => (open = false)}>
		<Dialog.Header class="min-w-0 pr-10 text-left">
			<Dialog.Title class="flex min-w-0 items-center gap-2 leading-normal" title={object.name}>
				<span class="icon-[clarity--objects-solid] h-6 w-6 shrink-0" aria-hidden="true"></span>
				<span class="truncate">Object Usages - {object.name}</span>
			</Dialog.Title>
		</Dialog.Header>

		<ul class="max-h-[60vh] divide-y overflow-y-auto rounded-md border">
			{#each labels as label, index (index)}
				{@const usage = object.usedBy?.[index]}
				{@const details = usage ? networkObjectUsageTypes[usage.type] : null}
				<li class="flex items-center gap-3 p-3">
					<span
						class="h-5 w-5 shrink-0 {details?.icon ?? 'icon-[mdi--link-variant]'} {details?.color ??
							'text-muted-foreground'}"
						aria-hidden="true"
					></span>
					<span class="min-w-0 flex-1 break-all text-sm font-medium">
						{usage?.name || label}
					</span>
					{#if details}
						<span
							class="inline-flex shrink-0 items-center rounded border px-1 py-0.5 font-mono text-xs leading-tight {details.color}"
						>
							{details.label}
						</span>
					{/if}
				</li>
			{/each}
		</ul>

		<Dialog.Footer class="sm:justify-start">
			<span class="text-muted-foreground text-xs">References: {labels.length}</span>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
