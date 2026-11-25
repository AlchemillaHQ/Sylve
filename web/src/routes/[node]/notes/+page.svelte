<script lang="ts">
	import ClassicNotes from '$lib/components/custom/Node/Notes.svelte';
	import Notes from '$lib/components/zen/Notes/Notes.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
</script>

{#if ui.zen}
	{#key data.hostname}
		<Notes {data} />
	{/key}
{:else}
	<ClassicNotes data={{ notes: Array.isArray(data.notes) ? data.notes : [] }} />
{/if}
