<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Snippet } from 'svelte';

	interface Props {
		open: boolean;
		title: string;
		icon: string;
		width?: string;
		onSave: () => Promise<boolean>;
		children: Snippet;
	}

	let { open = $bindable(), title, icon, width = '', onSave, children }: Props = $props();
	let saving = $state(false);
	let content = $state<HTMLElement | null>(null);

	async function save() {
		saving = true;
		if (await onSave()) open = false;
		saving = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		bind:ref={content}
		class="overflow-hidden p-6 {width}"
		showCloseButton={!saving}
		onOpenAutoFocus={(event) => {
			event.preventDefault();
			content
				?.querySelector<HTMLElement>('input:not([type="checkbox"]), textarea')
				?.focus({ preventScroll: true });
		}}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon {icon} size="h-5 w-5" gap="gap-2" {title} />
			</Dialog.Title>
		</Dialog.Header>

		<fieldset disabled={saving}>
			{@render children()}
		</fieldset>

		<Dialog.Footer>
			<Button onclick={save} size="sm" disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
