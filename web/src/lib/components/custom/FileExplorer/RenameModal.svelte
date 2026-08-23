<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Props {
		isOpen: boolean;
		newName: string;
		onClose: () => void;
		onReset: () => void;
		onRename: () => void;
		loading?: boolean;
	}

	let {
		isOpen = $bindable(false),
		newName = $bindable(''),
		onClose,
		onReset,
		onRename,
		loading = false
	}: Props = $props();
</script>

<Dialog.Root bind:open={isOpen}>
	<Dialog.Content
		onInteractOutside={(event) => {
			if (loading) event.preventDefault();
			else onClose();
		}}
		onEscapeKeydown={(event) => {
			if (loading) event.preventDefault();
		}}
		aria-busy={loading}
		class="fixed flex transform flex-col gap-4 overflow-auto p-6 transition-all duration-300 ease-in-out lg:max-w-md"
		showCloseButton={!loading}
		showResetButton={!loading}
		{onClose}
		{onReset}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--rename-box-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Rename"
				/>
			</Dialog.Title>
		</Dialog.Header>
		<div class="mt-2">
			<CustomValueInput
				placeholder="Enter new name"
				bind:value={newName}
				classes="flex-1 space-y-1.5"
			/>
		</div>
		<Dialog.Footer class="mt-2">
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28"
					onclick={onRename}
					disabled={loading || !newName.trim()}
				>
					{#if loading}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Renaming...
					{:else}
						Rename
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
