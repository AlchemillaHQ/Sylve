<script lang="ts">
	import * as Breadcrumb from '$lib/components/ui/breadcrumb';
	import Button from '$lib/components/ui/button/button.svelte';

	let { currentPath, onBackClick, items, onNavigate } = $props();
</script>

<header class="flex shrink-0 items-center gap-2 border-b px-4">
	<Button
		variant="ghost"
		size="icon"
		class="cursor-pointer"
		onclick={() => onBackClick()}
		disabled={currentPath === '/'}
	>
		<span class="icon-[tabler--arrow-left] pointer-events-none !h-6 !w-6"></span>
	</Button>

	<Breadcrumb.Root>
		<Breadcrumb.List>
			{#each items as item, index}
				<Breadcrumb.Item>
					{#if item.isLast}
						<Breadcrumb.Page>{item.name}</Breadcrumb.Page>
					{:else}
						<Breadcrumb.Link
							href="#"
							onclick={(e: any) => {
								e.preventDefault();
								onNavigate(item.path);
							}}
						>
							{item.name}
						</Breadcrumb.Link>
					{/if}
				</Breadcrumb.Item>
				{#if !item.isLast}
					<Breadcrumb.Separator />
				{/if}
			{/each}
		</Breadcrumb.List>
	</Breadcrumb.Root>
</header>
