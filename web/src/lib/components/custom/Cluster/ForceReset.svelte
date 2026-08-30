<script lang="ts">
	import { forceResetCluster, refreshClusterAfterLifecycleChange } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { connection, planClusterLeaveRestart } from '$lib/stores/api.svelte';
	import { getClusterLeaveErrorMessage } from '$lib/utils/cluster';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		nodeId: string;
		onRestart: () => void;
	}

	let { open = $bindable(), nodeId, onRestart }: Props = $props();
	let confirmation = $state('');
	let membershipAcknowledged = $state(false);
	let workloadsFenced = $state(false);
	let busy = $state(false);

	let confirmed = $derived(
		confirmation.trim() === nodeId.trim() && membershipAcknowledged && workloadsFenced
	);

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				confirmation = '';
				membershipAcknowledged = false;
				workloadsFenced = false;
				busy = false;
			}
		}
	);

	async function confirm() {
		if (!confirmed || busy) return;
		busy = true;
		const ownsRestartPlan = planClusterLeaveRestart();
		try {
			const response = await forceResetCluster(nodeId, membershipAcknowledged, workloadsFenced);
			if (response.error) {
				if (ownsRestartPlan) connection.plannedRestart = null;
				handleAPIError(response);
				const detail = Array.isArray(response.error)
					? response.error.join(', ')
					: String(response.error || response.message);
				toast.error(getClusterLeaveErrorMessage(`${response.message}: ${detail}`), {
					position: 'bottom-center'
				});
				return;
			}
			onRestart();
			await refreshClusterAfterLifecycleChange();
			open = false;
			toast.success('Local cluster state reset. Sylve is restarting…', {
				position: 'bottom-center'
			});
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--alert-octagon-outline]"
					size="h-6 w-6"
					gap="gap-2"
					title="Force Local Reset"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 text-sm">
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3">
				<p class="font-medium text-red-600 dark:text-red-400">This only resets this machine.</p>
				<p class="mt-1 text-muted-foreground">
					It does not remove this node from surviving Raft membership. Keep duplicate workloads and
					shared storage isolated.
				</p>
			</div>

			<div class="grid gap-1.5">
				<label for="force-reset-node-id" class="font-medium"
					>Type the local Node ID to confirm</label
				>
				<Input id="force-reset-node-id" bind:value={confirmation} placeholder={nodeId} />
				<p class="break-all font-mono text-xs text-muted-foreground">{nodeId}</p>
			</div>

			<CustomCheckbox
				label="Remote membership is repaired or will be repaired separately"
				bind:checked={membershipAcknowledged}
			/>
			<CustomCheckbox
				label="Duplicate workloads and shared storage are externally fenced"
				bind:checked={workloadsFenced}
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={() => (open = false)} size="sm" variant="outline" disabled={busy}>
					Cancel
				</Button>
				<Button onclick={confirm} size="sm" variant="destructive" disabled={!confirmed || busy}>
					{busy ? 'Resetting…' : 'Force Local Reset'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
