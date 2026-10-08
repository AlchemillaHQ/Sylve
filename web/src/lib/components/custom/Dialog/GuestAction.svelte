<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { onDestroy } from 'svelte';
	import AlertDialog from './Alert.svelte';
	import {
		cancelGuestActionConfirmation,
		finishGuestActionConfirmation,
		guestActionConfirmation
	} from '$lib/stores/guest-actions.svelte';

	const labels = {
		start: 'Start',
		stop: 'Stop',
		'force-stop': 'Force Stop',
		shutdown: 'Shut Down',
		reboot: 'Reboot',
		restart: 'Restart'
	};
	let target = $derived(guestActionConfirmation.pending);

	beforeNavigate(cancelGuestActionConfirmation);
	onDestroy(cancelGuestActionConfirmation);
</script>

{#if target}
	<AlertDialog
		open={true}
		title={`${labels[target.action]} ${target.guestType === 'vm' ? 'VM' : 'jail'}?`}
		description={target.action === 'stop' || target.action === 'force-stop'
			? 'Running workloads will be interrupted. Unsaved data may be lost.'
			: 'This will interrupt the guest’s running workloads.'}
		confirmLabel={labels[target.action]}
		onOpenChange={(open) => {
			if (!open) finishGuestActionConfirmation(false);
		}}
		actions={{
			onConfirm: () => finishGuestActionConfirmation(true),
			onCancel: () => finishGuestActionConfirmation(false)
		}}
	>
		<div class="bg-muted rounded-md p-3 text-sm">
			<p class="break-all font-semibold">{target.name} ({target.guestId})</p>
			<p class="text-muted-foreground break-all">Node: {target.hostname}</p>
		</div>
	</AlertDialog>
{/if}
