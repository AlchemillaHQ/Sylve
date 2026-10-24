<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { HostUpdateContext } from '$lib/types/system/updates';
	import { getContext, type Snippet } from 'svelte';

	const updates = getContext<HostUpdateContext>('hostUpdates');
	let { children, showRefresh = true }: { children?: Snippet; showRefresh?: boolean } = $props();
</script>

{#if updates.view?.activeJob}<Button
		onclick={() => updates.openOperation(updates.view!.activeJob!.id)}
		size="sm"
		variant="outline"
		class="h-6"
		title="View active operation"
	>
		<SpanWithIcon
			icon="icon-[mdi--loading] animate-spin"
			size="h-4 w-4"
			gap="gap-2"
			title="In progress"
		/>
	</Button>{/if}
{@render children?.()}
<Button
	onclick={updates.openMonitoring}
	size="sm"
	variant="outline"
	class="h-6"
	disabled={!updates.view}
	title="Monitoring"
	aria-label="Monitoring"><span class="icon-[mdi--clock-outline] h-4 w-4"></span></Button
>
{#if showRefresh}<Button
		onclick={updates.refresh}
		size="sm"
		variant="outline"
		class="h-6"
		disabled={updates.loading}
		title="Refresh inventory"
		aria-label="Refresh inventory"
		><span
			class={updates.loading
				? 'icon-[mdi--loading] h-4 w-4 animate-spin'
				: 'icon-[mdi--refresh] h-4 w-4'}
		></span></Button
	>{/if}
