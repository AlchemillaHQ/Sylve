<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';

	interface Props {
		open: boolean;
		names?: {
			parent: string;
			element: string;
		};
		actions: {
			onConfirm: () => void;
			onCancel: () => void;
		};
		customTitle?: string;
		confirmLabel?: string;
		loadingLabel?: string;
		loading?: boolean;
	}

	let {
		open = $bindable(),
		names,
		actions,
		customTitle,
		confirmLabel = 'Continue',
		loadingLabel = 'Processing...',
		loading = false
	}: Props = $props();
</script>

<AlertDialog.Root bind:open>
	<AlertDialog.Content onInteractOutside={(e) => e.preventDefault()} class="p-5">
		<AlertDialog.Header>
			<AlertDialog.Title>Are you sure?</AlertDialog.Title>
			<AlertDialog.Description>
				{#if customTitle}
					{@html customTitle}
				{:else if names && names.parent && names.element}
					{'This action cannot be undone. This will permanently delete '}
					<span>{names.parent}</span>
					<span class="font-semibold">{names.element}</span>.
				{/if}
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel onclick={actions.onCancel}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={actions.onConfirm} disabled={loading}>
				{#if loading}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					{loadingLabel}
				{:else}
					{confirmLabel}
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
