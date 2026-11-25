<script lang="ts">
	import { page } from '$app/state';
	import ClassicTerminal from '$lib/components/custom/Node/Terminal.svelte';
	import Terminal from '$lib/components/zen/Terminal/Terminal.svelte';
	import { ui } from '$lib/stores/interface.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
</script>

{#if ui.zen}
	{#if allowed('Sys.Console', nodePath(page.params.node || ''))}
		{#key page.params.node}
			<Terminal node={page.params.node || ''} />
		{/key}
	{:else}
		<div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
			<span class="icon-[lucide--lock-keyhole] text-muted-foreground size-10" aria-hidden="true"
			></span>
			<h1 class="text-lg font-semibold">Terminal Access Required</h1>
			<p class="text-muted-foreground text-sm">
				You do not have permission to open this node's terminal.
			</p>
		</div>
	{/if}
{:else}
	<ClassicTerminal />
{/if}
