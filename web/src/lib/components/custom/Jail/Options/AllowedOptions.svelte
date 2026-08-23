<script lang="ts">
	import { modifyAllowedOptions } from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		node: string;
		onSaved: () => void | Promise<void>;
		devFSDisabled?: boolean;
	}

	let { open = $bindable(), jail, node, onSaved, devFSDisabled = false }: Props = $props();

	const allowed: { value: string; label: string }[] = [
		{ value: 'allow.adjtime', label: 'Adjust Time (allow.adjtime)' },
		{ value: 'allow.chflags', label: 'Change File Flags (allow.chflags)' },
		{ value: 'allow.extattr', label: 'Extended Attributes (allow.extattr)' },
		{ value: 'allow.mlock', label: 'Memory Locking (allow.mlock)' },
		{ value: 'allow.mount', label: 'Mount Filesystems (allow.mount)' },
		{ value: 'allow.mount.devfs', label: 'Mount devfs (allow.mount.devfs)' },
		{ value: 'allow.mount.fdescfs', label: 'Mount fdescfs (allow.mount.fdescfs)' },
		{ value: 'allow.mount.fusefs', label: 'Mount fusefs (allow.mount.fusefs)' },
		{ value: 'allow.mount.linprocfs', label: 'Mount linprocfs (allow.mount.linprocfs)' },
		{ value: 'allow.mount.linsysfs', label: 'Mount linsysfs (allow.mount.linsysfs)' },
		{ value: 'allow.mount.nullfs', label: 'Mount nullfs (allow.mount.nullfs)' },
		{ value: 'allow.mount.procfs', label: 'Mount procfs (allow.mount.procfs)' },
		{ value: 'allow.mount.tmpfs', label: 'Mount tmpfs (allow.mount.tmpfs)' },
		{ value: 'allow.mount.zfs', label: 'Mount ZFS (allow.mount.zfs)' },
		{ value: 'allow.nfsd', label: 'NFS Daemon (allow.nfsd)' },
		{ value: 'allow.quotas', label: 'FS Quotas (allow.quotas)' },
		{ value: 'allow.raw_sockets', label: 'Raw Sockets (allow.raw_sockets)' },
		{ value: 'allow.read_msgbuf', label: 'Read Kernel Message Buffer (allow.read_msgbuf)' },
		{ value: 'allow.reserved_ports', label: 'Reserved Ports (allow.reserved_ports)' },
		{ value: 'allow.routing', label: 'Routing (allow.routing)' },
		{ value: 'allow.set_hostname', label: 'Set Hostname (allow.set_hostname)' },
		{ value: 'allow.setaudit', label: 'Set Audit (allow.setaudit)' },
		{ value: 'allow.settime', label: 'Set Time (allow.settime)' },
		{ value: 'allow.socket_af', label: 'Socket Address Families (allow.socket_af)' },
		{ value: 'allow.suser', label: 'Super User Privileges (allow.suser)' },
		{ value: 'allow.sysvipc', label: 'SysV IPC (allow.sysvipc)' },
		{
			value: 'allow.unprivileged_parent_tampering',
			label: 'Unprivileged Parent Tampering (allow.unprivileged_parent_tampering)'
		},
		{
			value: 'allow.unprivileged_proc_debug',
			label: 'Unprivileged Process Debugging (allow.unprivileged_proc_debug)'
		},
		{ value: 'allow.vmm', label: 'Virtual Machines (allow.vmm)' }
	];

	function initialOptions(): string[] {
		return (jail.allowedOptions || []).filter(
			(option) => !devFSDisabled || option !== 'allow.mount.devfs'
		);
	}

	let filteredAllowed = $derived(
		devFSDisabled ? allowed.filter((option) => option.value !== 'allow.mount.devfs') : allowed
	);
	let removesUnavailableDevFSOption = $derived(
		devFSDisabled && (jail.allowedOptions || []).includes('allow.mount.devfs')
	);
	let comboOpen = $state(false);
	let selectedOptions = $state<string[]>(initialOptions());
	let saving = $state(false);

	function reset() {
		selectedOptions = initialOptions();
	}

	async function save() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyAllowedOptions(jail.ctId, selectedOptions, {
				hostname: node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to save allowed options', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('Allowed options saved', { position: 'bottom-center' });
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 overflow-hidden p-6 lg:max-w-lg"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (saving) return;
			reset();
			open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
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
			{#if removesUnavailableDevFSOption}
				<p class="text-muted-foreground text-xs">
					DevFS management is disabled on this host. Saving removes the unavailable
					<code>allow.mount.devfs</code> option.
				</p>
			{/if}
			<CustomComboBoxBindable
				bind:open={comboOpen}
				label=""
				placeholder="Select Allowed Options"
				bind:value={selectedOptions}
				data={[...filteredAllowed]}
				multiple={true}
				classes="w-full"
				width="w-full"
				showSelected={false}
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm" disabled={saving} aria-busy={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
