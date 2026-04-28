<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import SpanWithIcon from '../SpanWithIcon.svelte';

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
			<AlertDialog.Title>
				<SpanWithIcon
					icon="icon-[lucide--alert-triangle]"
					size="h-5 w-5"
					gap="gap-2"
					title="Are you sure?"
				/>
			</AlertDialog.Title>
			<AlertDialog.Description>
				{#if customTitle}
					<!-- eslint-disable-next-line svelte/no-at-html-tags -->
					{@html customTitle}
				{:else if names && names.parent && names.element}
					<!-- eslint-disable-next-line svelte/no-useless-mustaches -->
					<span>This action cannot be undone. This will permanently delete {''}</span><span
						class="break-all"
						>{names.parent} <span class="font-semibold">{names.element}</span></span
					>.
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
