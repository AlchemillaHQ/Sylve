<script lang="ts">
	import { cn, type WithoutChildrenOrChild } from '$lib/utils.js';
	import { Dialog as DialogPrimitive } from 'bits-ui';
	import type { Snippet } from 'svelte';
	import * as Dialog from './index.js';

	let {
		ref = $bindable(null),
		class: className,
		portalProps,
		children,
		overlayClass,
		showCloseButton = true,
		showResetButton = false,
		onClose,
		onReset,
		...restProps
	}: WithoutChildrenOrChild<DialogPrimitive.ContentProps> & {
		portalProps?: DialogPrimitive.PortalProps;
		children: Snippet;
		showCloseButton?: boolean;
		showResetButton?: boolean;
		onClose?: () => void;
		onReset?: () => void;
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
		{...restProps}
	>
		{@render children?.()}
		{#if showCloseButton || showResetButton}
			<div class="absolute top-6 right-6 flex items-center gap-2">
				{#if showResetButton}
					<button
						onclick={onReset}
						class="opacity-50 transition-opacity hover:opacity-100 focus:outline-none disabled:pointer-events-none"
					>
						<span class="icon-[radix-icons--reset] h-5 w-5"></span>
						<span class="sr-only">Reset</span>
					</button>
				{/if}
				{#if showCloseButton}
					<DialogPrimitive.Close
						onclick={onClose}
						class="opacity-50 transition-opacity hover:opacity-100 focus:outline-none disabled:pointer-events-none"
						data-slot="dialog-close"
					>
						<span class="icon-[lucide--x] h-5 w-5"></span>
						<span class="sr-only">Close</span>
					</DialogPrimitive.Close>
				{/if}
			</div>
		{/if}
	</DialogPrimitive.Content>
</Dialog.Portal>
