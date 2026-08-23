<script lang="ts">
	import { bulkDelete } from '$lib/api/zfs/datasets';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		reload?: boolean;
	}

	let { open = $bindable(), datasets, reload = $bindable() }: Props = $props();

	function onCancel() {
		open = false;
	}

	async function onConfirm() {
		const targets = [...datasets];
		if (targets.length > 0) {
			const response = await bulkDelete(targets);

			if (response.status === 'success') {
				open = false;
				toast.success(`Deleted ${targets.length} snapshots`, {
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

<AlertDialog
	bind:open
	customTitle={`This will permanently delete ${datasets.length} selected snapshot${datasets.length === 1 ? '' : 's'}. This action cannot be undone.`}
	loadingLabel="Deleting snapshots..."
	actions={{ onConfirm, onCancel }}
/>
