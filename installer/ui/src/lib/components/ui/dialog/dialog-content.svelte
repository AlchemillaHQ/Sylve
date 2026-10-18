<script lang="ts">
	import { cn, type WithoutChildrenOrChild } from '$lib/utils.js';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import type { Snippet } from 'svelte';
	import * as Dialog from './index.js';
	import X from '@lucide/svelte/icons/x';

	let {
		ref = $bindable(null),
		class: className,
		portalProps,
		children,
		overlayClass,
		showCloseButton = true,
		onClose,
		interactOutsideBehavior = 'ignore',
		...restProps
	}: WithoutChildrenOrChild<DialogPrimitive.ContentProps> & {
		portalProps?: DialogPrimitive.PortalProps;
		children: Snippet;
		showCloseButton?: boolean;
		onClose?: () => void;
		overlayClass?: string;
	} = $props();
</script>

<Dialog.Portal {...portalProps}>
	<Dialog.Overlay class={overlayClass} />
	<DialogPrimitive.Content
		bind:ref
		data-slot="dialog-content"
		class={cn(
			'bg-background data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 fixed top-[50%] left-[50%] z-50 grid w-full max-w-[calc(100%-2rem)] translate-x-[-50%] translate-y-[-50%] gap-4 rounded-lg border p-6 shadow-lg duration-200 sm:max-w-lg',
			className
		)}
		{interactOutsideBehavior}
		{...restProps}
	>
		{@render children?.()}
		{#if showCloseButton}
			<div class="absolute top-6 right-6 flex items-center gap-2">
				<DialogPrimitive.Close
					onclick={onClose}
					class="text-muted-foreground transition-colors hover:text-foreground focus:outline-none disabled:pointer-events-none"
					data-slot="dialog-close"
				>
					<X class="size-4" />
					<span class="sr-only">Close</span>
				</DialogPrimitive.Close>
			</div>
		{/if}
	</DialogPrimitive.Content>
</Dialog.Portal>
