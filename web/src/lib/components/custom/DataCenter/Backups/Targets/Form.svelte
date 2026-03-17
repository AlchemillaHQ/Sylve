<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import type { BackupTarget } from '$lib/types/cluster/backups';
	import { toast } from 'svelte-sonner';
	import {
		createBackupTarget,
		updateBackupTarget,
		type BackupTargetInput
	} from '$lib/api/cluster/backups';
	import { handleAPIError } from '$lib/utils/http';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		edit: boolean;
		name: string;
		sshHost: string;
		sshPort: number;
		sshKey: string;
		backupRoot: string;
		description: string;
		createBackupRoot: boolean;
		enabled: boolean;
		reload: boolean;
		selectedTarget: BackupTarget | null;
	}

	let {
		open = $bindable(),
		edit = $bindable(),
		name = $bindable(),
		sshHost = $bindable(),
		sshPort = $bindable(),
		sshKey = $bindable(),
		backupRoot = $bindable(),
		description = $bindable(),
		createBackupRoot = $bindable(),
		enabled = $bindable(),
		reload = $bindable(),
		selectedTarget
	}: Props = $props();

	let loading = $state(false);

	async function saveTarget() {
		if (!name.trim()) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (!sshHost.trim()) {
			toast.error('SSH Host is required', { position: 'bottom-center' });
			return;
		}
		if (!backupRoot.trim()) {
			toast.error('Backup Root is required', { position: 'bottom-center' });
			return;
		}

		const payload: BackupTargetInput = {
			name: name,
			sshHost: sshHost,
			sshPort: sshPort || 22,
			sshKey: sshKey || undefined,
			backupRoot: backupRoot,
			createBackupRoot: createBackupRoot,
			description: description,
			enabled: enabled
		};

		loading = true;

		const response = edit
			? await updateBackupTarget(selectedTarget?.id || 0, payload)
			: await createBackupTarget(payload);

		loading = false;

		if (response.status === 'success') {
			toast.success(edit ? 'Backup target updated' : 'Backup target created', {
				position: 'bottom-center'
			});

			open = false;
			reload = true;
			return;
		}

		handleAPIError(response);
		if (response.error?.includes('backup_root_not_found')) {
			toast.error('Backup root not found on target', { position: 'bottom-center' });
		} else {
			toast.error(edit ? 'Failed to update target' : 'Failed to create target', {
				position: 'bottom-center'
			});
		}
	}

	function reset(values: boolean) {
		if (values && edit) {
			open = true;
		} else {
			open = false;
		}

		name = edit ? selectedTarget?.name || '' : '';
		sshHost = edit ? selectedTarget?.sshHost || '' : '';
		sshPort = edit ? selectedTarget?.sshPort || 22 : 22;
		sshKey = '';
		backupRoot = edit ? selectedTarget?.backupRoot || '' : '';
		createBackupRoot = edit ? selectedTarget?.createBackupRoot || false : false;
		description = edit ? selectedTarget?.description || '' : '';
		enabled = edit ? selectedTarget?.enabled || true : true;
	}

	watch(
		() => open,
		(value) => {
			if (value) {
				name = edit ? selectedTarget?.name || '' : '';
				sshHost = edit ? selectedTarget?.sshHost || '' : '';
				sshPort = edit ? selectedTarget?.sshPort || 22 : 22;
				sshKey = '';
				backupRoot = edit ? selectedTarget?.backupRoot || '' : '';
				createBackupRoot = edit ? selectedTarget?.createBackupRoot || false : false;
				description = edit ? selectedTarget?.description || '' : '';
				enabled = edit ? selectedTarget?.enabled || true : true;
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title class="flex justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[carbon--server-proxy] h-5 w-5"></span>
					<span>{edit ? 'Edit Backup Target' : 'New Backup Target'}</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 {edit ? '' : 'hidden'}"
						onclick={() => {
							if (edit) {
								reset(true);
							}
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							reset(false);
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<CustomValueInput
				label="Name"
				placeholder="offsite-backup"
				bind:value={name}
				classes="space-y-1"
			/>

			<div class="grid grid-cols-3 gap-3">
				<div class="col-span-2">
					<CustomValueInput
						label="SSH Host"
						placeholder="root@192.168.1.100"
						bind:value={sshHost}
						classes="space-y-1"
					/>
				</div>
				<CustomValueInput
					label="SSH Port"
					placeholder="22"
					bind:value={sshPort}
					type="number"
					classes="space-y-1"
				/>
			</div>

			<div class="space-y-1">
				<CustomValueInput
					label="SSH Private Key {edit ? '(leave empty to keep existing)' : ''}"
					placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
					bind:value={sshKey}
					type="textarea"
					classes="space-y-1"
					textAreaClasses="min-h-[80px]! max-h-[200px]!"
				/>
			</div>

			<CustomValueInput
				label="Backup Root"
				placeholder="tank/Backups"
				bind:value={backupRoot}
				classes="space-y-1"
			/>

			<CustomValueInput
				label="Description"
				placeholder="Offsite backup server in datacenter"
				bind:value={description}
				classes="space-y-1"
			/>

			<div class="flex items-center gap-4">
				<CustomCheckbox
					label="Create Backup Root"
					bind:checked={createBackupRoot}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox label="Enabled" bind:checked={enabled} classes="flex items-center gap-2" />
			</div>
		</div>

		<Dialog.Footer>
			<Button
				variant="outline"
				onclick={() => {
					reset(false);
				}}
				disabled={loading}
			>
				Cancel
			</Button>
			<Button onclick={saveTarget} disabled={loading}>
				{#if loading}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						<span>{edit ? 'Updating' : 'Creating'}</span>
					</div>
				{:else}
					{edit ? 'Update' : 'Create'}
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
