<script lang="ts">
	import { ScrollArea as ScrollAreaPrimitive } from 'bits-ui';
	import { Scrollbar } from './index.js';
	import { cn, type WithoutChild } from '$lib/utils.js';

	let {
		ref = $bindable(null),
		class: className,
		orientation = 'vertical',
		scrollbarXClasses = '',
		scrollbarYClasses = '',
		fade = false,
		children,
		...restProps
	}: WithoutChild<ScrollAreaPrimitive.RootProps> & {
		orientation?: 'vertical' | 'horizontal' | 'both' | undefined;
		scrollbarXClasses?: string | undefined;
		scrollbarYClasses?: string | undefined;
		// Fades the bottom edge while there's more to scroll to.
		fade?: boolean;
	} = $props();

	let viewport = $state<HTMLElement | null>(null);
	let more = $state(false);

	function measure() {
		if (viewport) more = viewport.scrollTop + viewport.clientHeight < viewport.scrollHeight - 1;
	}

	$effect(() => {
		if (!fade || !viewport) return;
		const observer = new ResizeObserver(measure);
		observer.observe(viewport);
		if (viewport.firstElementChild) observer.observe(viewport.firstElementChild);
		return () => observer.disconnect();
	});
</script>

<ScrollAreaPrimitive.Root
	bind:ref
	data-slot="scroll-area"
	class={cn('relative', className)}
	{...restProps}
>
	<ScrollAreaPrimitive.Viewport
		bind:ref={viewport}
		data-slot="scroll-area-viewport"
		class="ring-ring/10 dark:ring-ring/20 dark:outline-ring/40 outline-ring/50 size-full rounded-[inherit] transition-[color,box-shadow] focus-visible:outline-1 focus-visible:ring-4"
		onscroll={fade ? measure : undefined}
	>
		{@render children?.()}
	</ScrollAreaPrimitive.Viewport>
	{#if fade}
		<div
			class="from-background pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t to-transparent transition-opacity duration-200 {more
				? 'opacity-100'
				: 'opacity-0'}"
		></div>
	{/if}
	{#if orientation === 'vertical' || orientation === 'both'}
		<Scrollbar orientation="vertical" class={scrollbarYClasses} />
	{/if}
	{#if orientation === 'horizontal' || orientation === 'both'}
		<Scrollbar orientation="horizontal" class={scrollbarXClasses} />
	{/if}
	<ScrollAreaPrimitive.Corner />
</ScrollAreaPrimitive.Root>
