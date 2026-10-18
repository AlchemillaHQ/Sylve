<script lang="ts">
	import { cn } from '$lib/utils.js';
	import { Tabs as TabsPrimitive } from 'bits-ui';

	let { ref = $bindable(null), class: className, ...restProps }: TabsPrimitive.ListProps = $props();
</script>

<TabsPrimitive.List
	bind:ref
	data-slot="tabs-list"
	class={cn(
		'bg-background dark:bg-muted text-muted-foreground inline-flex h-9 w-fit items-center justify-center rounded-lg border p-[3px] dark:border-none',
		className
	)}
	{...restProps}
/>
