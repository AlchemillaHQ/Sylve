<script lang="ts">
	import { modifyAllowedOptions } from '$lib/api/jail/jail';
	import CustomComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), jail, reload = $bindable() }: Props = $props();

	const allowed: { value: string; label: string }[] = [
		{ value: 'allow.set_hostname', label: 'Set Hostname (allow.set_hostname)' },
		{ value: 'allow.raw_sockets', label: 'Raw Sockets (allow.raw_sockets)' },
		{ value: 'allow.chflags', label: 'Change File Flags (allow.chflags)' },
		{ value: 'allow.mount', label: 'Mount Filesystems (allow.mount)' },
		{ value: 'allow.mount.devfs', label: 'Mount devfs (allow.mount.devfs)' },
		{ value: 'allow.quotas', label: 'FS Quotas (allow.quotas)' },
		{ value: 'allow.read_msgbuf', label: 'Read Kernel Message Buffer (allow.read_msgbuf)' },
		{ value: 'allow.socket_af', label: 'Socket Address Families (allow.socket_af)' },
		{ value: 'allow.mlock', label: 'Memory Locking (allow.mlock)' },
		{ value: 'allow.nfsd', label: 'NFS Daemon (allow.nfsd)' },
		{ value: 'allow.reserved_ports', label: 'Reserved Ports (allow.reserved_ports)' },
		{
			value: 'allow.unprivileged_proc_debug',
			label: 'Unprivileged Process Debugging (allow.unprivileged_proc_debug)'
		},
		{ value: 'allow.mount.fdescfs', label: 'Mount fdescfs (allow.mount.fdescfs)' },
		{ value: 'allow.mount.fusefs', label: 'Mount fusefs (allow.mount.fusefs)' },
		{ value: 'allow.mount.nullfs', label: 'Mount nullfs (allow.mount.nullfs)' },
		{ value: 'allow.mount.procfs', label: 'Mount procfs (allow.mount.procfs)' },
		{ value: 'allow.mount.linprocfs', label: 'Mount linprocfs (allow.mount.linprocfs)' },
		{ value: 'allow.mount.linsysfs', label: 'Mount linsysfs (allow.mount.linsysfs)' },
		{ value: 'allow.mount.tmpfs', label: 'Mount tmpfs (allow.mount.tmpfs)' },
		{ value: 'allow.mount.zfs', label: 'Mount ZFS (allow.mount.zfs)' },
		{ value: 'allow.vmm', label: 'Virtual Machines (allow.vmm)' }
	];

	let comboOpen = $state(false);
	let selectedOptions = $state<string[]>([]);

	watch(
		() => jail.allowedOptions,
		(newVal) => {
			selectedOptions = [...(newVal || [])];
		}
	);

	async function save() {
		const response = await modifyAllowedOptions(jail.ctId, selectedOptions);
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to save allowed options', { position: 'bottom-center' });
			return;
		}

		toast.success('Allowed options saved', { position: 'bottom-center' });
		reload = !reload;
		open = false;
	}

	function reset() {
		selectedOptions = [...(jail.allowedOptions || [])];
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 overflow-hidden p-6 lg:max-w-lg"
		showResetButton={true}
		onReset={reset}
		onClose={() => {
			reset();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[material-symbols--rule-settings]"
					size="h-5 w-5"
					gap="gap-2"
					title="Allowed Options"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-3">
			<CustomComboBoxBindable
				bind:open={comboOpen}
				label=""
				placeholder="Select Allowed Options"
				bind:value={selectedOptions}
				data={[...allowed]}
				multiple={true}
				classes="w-full"
				width="w-full"
				showSelected={false}
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm">Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
