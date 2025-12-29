<script lang="ts">
	import { bulkDeleteByNames } from '$lib/api/zfs/datasets';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		askRecursive?: boolean;
		reload?: boolean;
	}

	let { open = $bindable(), datasets, askRecursive = true, reload = $bindable() }: Props = $props();
	let recursive = $state(false);

	async function onCancel() {
		open = false;
	}

	async function onConfirm() {
		if (datasets.length > 0) {
			const response = await bulkDeleteByNames(datasets);

			if (response.status === 'success') {
				open = false;
				toast.success(`Deleted ${datasets.length} snapshots`, {
					position: 'bottom-center'
				});
			} else {
				handleAPIError(response);
				toast.error(`Failed to delete snapshots`, {
					position: 'bottom-center'
				});
			}
		} else {
			toast.error('Snapshot GUID not found', {
				position: 'bottom-center'
			});
		}

		reload = true;
	}
</script>

<AlertDialog.Root bind:open>
	<AlertDialog.Content onInteractOutside={(e) => e.preventDefault()}>
		<AlertDialog.Header>
			<AlertDialog.Title>Are you sure?</AlertDialog.Title>
			<AlertDialog.Description>
				{#if datasets.length === 1}
					<b>This will delete snapshot {datasets[0].name}</b>
				{:else}
					<b>This will delete {datasets.length} snapshots</b>
				{/if}
			</AlertDialog.Description>
		</AlertDialog.Header>

		{#if askRecursive}
			<CustomCheckbox label="Recursive" bind:checked={recursive} classes="flex items-center gap-2"
			></CustomCheckbox>
		{/if}
		<AlertDialog.Footer>
			<AlertDialog.Cancel onclick={onCancel}>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={onConfirm}>Continue</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
