<script lang="ts">
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { onMount } from 'svelte';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		names?: {
			parent: string;
			element: string;
		};
		actions: {
			onConfirm: () => void | Promise<void>;
			onCancel: () => void;
		};
		customTitle?: string;
		confirmLabel?: string;
		loadingLabel?: string;
		loading?: boolean;
		keepOpenOnConfirm?: boolean;
	}

	let {
		open = $bindable(),
		names,
		actions,
		customTitle,
		confirmLabel = 'Continue',
		loadingLabel = 'Processing...',
		loading = false,
		keepOpenOnConfirm = false
	}: Props = $props();

	let confirming = $state(false);
	let mounted = false;
	let busy = $derived(loading || confirming);

	onMount(() => {
		mounted = true;
		return () => {
			mounted = false;
		};
	});

	async function handleConfirm(event: MouseEvent) {
		if (busy) return;
		if (keepOpenOnConfirm) event.preventDefault();
		confirming = true;
		try {
			await actions.onConfirm();
		} finally {
			if (mounted) confirming = false;
		}
	}

	function handleCancel() {
		if (busy) return;
		actions.onCancel();
	}

	function handleEscapeKeydown(event: KeyboardEvent) {
		if (busy) event.preventDefault();
	}
</script>

<AlertDialog.Root bind:open>
	<AlertDialog.Content
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={handleEscapeKeydown}
		aria-busy={busy}
		class="p-5"
	>
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
			<AlertDialog.Cancel onclick={handleCancel} disabled={busy}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={handleConfirm} disabled={busy}>
				{#if busy}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					{loadingLabel}
				{:else}
					{confirmLabel}
				{/if}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
