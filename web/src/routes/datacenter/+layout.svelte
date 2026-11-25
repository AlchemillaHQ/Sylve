<script lang="ts">
	import ClassicLayout from '$lib/components/custom/DataCenter/Layout.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children?: Snippet } = $props();
</script>

{#if ui.zen}
	{@render children?.()}
{:else}
	<ClassicLayout {children} />
{/if}
