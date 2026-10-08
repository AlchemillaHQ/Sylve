<script lang="ts">
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import LoadingDialog from '$lib/components/custom/Dialog/Loading.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { reinitializeVMConfig } from '$lib/api/vm/vm';
	import { reload } from '$lib/stores/api.svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		rid: number;
		hostname: string;
		oncomplete: () => void | Promise<void>;
	}

	let { rid, hostname, oncomplete }: Props = $props();
	let open = $state(false);
	let loading = $state(false);

	async function handleReinitialize() {
		if (loading) return;
		open = false;
		loading = true;

		try {
			const result = await reinitializeVMConfig(rid, hostname);
			if (result.status === 'error') {
				let message = 'Error reinitializing libvirt config';
				switch (result.message) {
					case 'vm_not_orphaned':
						message = 'This VM already has a libvirt configuration';
						break;
					case 'lifecycle_task_in_progress':
						message = 'VM action already in progress';
						break;
					case 'replication_lease_not_owned':
					case 'guest_identity_claim_conflict':
						message = 'This node does not own the right to restore this VM';
						break;
					case 'libvirt_connection_unavailable':
						message = 'Libvirt is unavailable; try again when it is running';
						break;
				}
				toast.error(message, { duration: 5000, position: 'bottom-center' });
			} else {
				reload.leftPanel = true;
				toast.success('Libvirt config reinitialized; VM has not been started', {
					duration: 5000,
					position: 'bottom-center'
				});
			}
			await oncomplete();
		} finally {
			loading = false;
		}
	}
</script>

<Button
	onclick={() => (open = true)}
	disabled={loading}
	size="sm"
	class="bg-muted-foreground/40 dark:bg-muted h-6 text-black hover:bg-blue-600 dark:text-white"
>
	<SpanWithIcon
		icon="icon-[mdi--restore]"
		size="h-4 w-4"
		gap="gap-1"
		title="Reinitialize libvirt config"
	/>
</Button>

<AlertDialog
	bind:open
	title={`Reinitialize libvirt config for VM ${rid}?`}
	customTitle="Rebuild the missing libvirt configuration from its saved Sylve settings. Existing disks and UEFI/TPM state will be preserved. The VM will not be started."
	confirmLabel="Reinitialize"
	loadingLabel="Reinitializing..."
	actions={{
		onConfirm: handleReinitialize,
		onCancel: () => (open = false)
	}}
/>

<LoadingDialog
	bind:open={loading}
	title="Reinitializing libvirt config"
	description="Restoring the VM definition from its saved settings without changing its disks."
	iconColor="text-blue-500"
/>
