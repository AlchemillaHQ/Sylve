<script lang="ts">
	import { createSambaShare, updateSambaShare } from '$lib/api/samba/share';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { Group, User } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { SambaShare } from '$lib/types/samba/shares';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
		users: User[];
		share?: SambaShare | null;
		edit?: boolean;
		reload?: boolean;
		appleExtensions?: boolean;
	}

	let {
		open = $bindable(),
		shares,
		datasets,
		groups,
		users,
		share,
		edit = false,
		reload = $bindable(),
		appleExtensions = false
	}: Props = $props();

	const userOptions = users.map((user) => ({
		label: user.username,
		value: String(user.id)
	}));

	const groupOptions = groups.map((group) => ({
		label: group.name,
		value: String(group.id)
	}));

	// svelte-ignore state_referenced_locally
	let options = {
		name: share ? share.name : '',
		dataset: {
			combobox: {
				open: false,
				value: share ? share.dataset : '',
				options: datasets
					.filter(
						(dataset) =>
							dataset.mountpoint !== '-' &&
							dataset.mountpoint !== null &&
							dataset.mountpoint !== '' &&
							dataset.mountpoint !== '/'
					)
					.map((dataset) => ({
						label: dataset.name,
						value: dataset.guid ? dataset.guid : dataset.name
					}))
			}
		},
		readUsers: {
			combobox: {
				open: false,
				value: share
					? share.permissions.read.users.map((user) => String(user.id))
					: ([] as string[]),
				options: userOptions
			}
		},
		writeUsers: {
			combobox: {
				open: false,
				value: share
					? share.permissions.write.users.map((user) => String(user.id))
					: ([] as string[]),
				options: userOptions
			}
		},
		readGroups: {
			combobox: {
				open: false,
				value: share
					? share.permissions.read.groups.map((group) => String(group.id))
					: ([] as string[]),
				options: groupOptions
			}
		},
		writeGroups: {
			combobox: {
				open: false,
				value: share
					? share.permissions.write.groups.map((group) => String(group.id))
					: ([] as string[]),
				options: groupOptions
			}
		},
		createMask: share ? share.createMask : '0664',
		directoryMask: share ? share.directoryMask : '2775',
		guest: {
			enabled: share ? share.guest.enabled : false,
			writeable: share ? share.guest.writeable : false
		},
		timeMachine: share ? share.timeMachine : false,
		timeMachineMaxSize: share ? share.timeMachineMaxSize : 0
	};

	let properties = $state(options);

	function normalizeWriteWins() {
		const writeUsers = new Set(properties.writeUsers.combobox.value);
		properties.readUsers.combobox.value = properties.readUsers.combobox.value.filter(
			(id) => !writeUsers.has(id)
		);

		const writeGroups = new Set(properties.writeGroups.combobox.value);
		properties.readGroups.combobox.value = properties.readGroups.combobox.value.filter(
			(id) => !writeGroups.has(id)
		);
	}

	function toIDList(values: string[]): number[] {
		return values
			.map((value) => Number(value))
			.filter((value) => Number.isFinite(value) && value > 0);
	}

	async function createOrEdit() {
		let error = '';

		if (shares.some((share) => share.name === properties.name) && share?.name !== properties.name) {
			error = 'Share name already exists';
		}

		if (properties.name === '') {
			error = 'Name is required';
		} else if (properties.dataset.combobox.value === '') {
			error = 'Dataset is required';
		}

		const totalPrincipals =
			properties.readUsers.combobox.value.length +
			properties.writeUsers.combobox.value.length +
			properties.readGroups.combobox.value.length +
			properties.writeGroups.combobox.value.length;

		if (!properties.guest.enabled && totalPrincipals === 0) {
			error = 'Select at least one user or group for authenticated access';
		}

		if (properties.guest.enabled && totalPrincipals > 0) {
			error = 'Guest-only shares cannot include users or groups';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const permissions = {
			read: {
				userIds: toIDList(properties.readUsers.combobox.value),
				groupIds: toIDList(properties.readGroups.combobox.value)
			},
			write: {
				userIds: toIDList(properties.writeUsers.combobox.value),
				groupIds: toIDList(properties.writeGroups.combobox.value)
			}
		};

		const guest = {
			enabled: properties.guest.enabled,
			writeable: properties.guest.writeable
		};

		let response: APIResponse;

		if (edit) {
			response = await updateSambaShare(
				share!.id,
				properties.name,
				properties.dataset.combobox.value,
				permissions,
				guest,
				properties.createMask,
				properties.directoryMask,
				properties.timeMachine,
				Number(properties.timeMachineMaxSize)
			);
		} else {
			response = await createSambaShare(
				properties.name,
				properties.dataset.combobox.value,
				permissions,
				guest,
				properties.createMask,
				properties.directoryMask,
				properties.timeMachine,
				Number(properties.timeMachineMaxSize)
			);
		}

		reload = true;

		if (response.status === 'error') {
			toast.error(`Failed to ${edit ? 'edit' : 'create'} Samba share`, {
				position: 'bottom-center'
			});
			return;
		}

		toast.success(`Samba share ${edit ? 'edited' : 'created'}`, {
			position: 'bottom-center'
		});

		open = false;
		properties = options;
	}

	watch(
		() => [
			() => properties.guest.enabled,
			() => properties.readUsers.combobox.value,
			() => properties.writeUsers.combobox.value,
			() => properties.readGroups.combobox.value,
			() => properties.writeGroups.combobox.value
		],
		() => {
			if (properties.guest.enabled) {
				if (properties.readUsers.combobox.value.length > 0) {
					properties.readUsers.combobox.value = [];
				}
				if (properties.writeUsers.combobox.value.length > 0) {
					properties.writeUsers.combobox.value = [];
				}
				if (properties.readGroups.combobox.value.length > 0) {
					properties.readGroups.combobox.value = [];
				}
				if (properties.writeGroups.combobox.value.length > 0) {
					properties.writeGroups.combobox.value = [];
				}
			}

			normalizeWriteWins();
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex flex-col p-5"
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--folder-network] h-6 w-6"></span>

					{#if edit}
						<span>Edit Samba Share</span>
					{:else}
						<span>Create Samba Share</span>
					{/if}
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title="Reset"
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title="Close"
						onclick={() => {
							open = false;
							properties = options;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomValueInput
				label="Name"
				placeholder="share"
				bind:value={properties.name}
				classes="flex-1 space-y-1.5"
			/>

			<CustomComboBox
				label="Dataset"
				placeholder="Select dataset"
				bind:open={properties.dataset.combobox.open}
				bind:value={properties.dataset.combobox.value}
				data={properties.dataset.combobox.options}
				width="w-2/5"
			/>

			<div class="flex items-center justify-between gap-2 rounded border p-2 md:col-span-2">
				<Label for="guest-mode">Guest Only</Label>
				<Checkbox id="guest-mode" bind:checked={properties.guest.enabled} />
			</div>

			{#if !properties.guest.enabled}
				<CustomComboBox
					label="Read Users"
					placeholder="Select users"
					bind:open={properties.readUsers.combobox.open}
					bind:value={properties.readUsers.combobox.value}
					data={properties.readUsers.combobox.options}
					multiple={true}
					showCount={true}
					showCountLabel=" users"
					width="w-2/5"
				/>

				<CustomComboBox
					label="Write Users"
					placeholder="Select users"
					bind:open={properties.writeUsers.combobox.open}
					bind:value={properties.writeUsers.combobox.value}
					data={properties.writeUsers.combobox.options}
					multiple={true}
					showCount={true}
					showCountLabel=" users"
					width="w-2/5"
				/>

				<CustomComboBox
					label="Read Groups"
					placeholder="Select groups"
					bind:open={properties.readGroups.combobox.open}
					bind:value={properties.readGroups.combobox.value}
					data={properties.readGroups.combobox.options}
					multiple={true}
					showCount={true}
					showCountLabel=" groups"
					width="w-2/5"
				/>

				<CustomComboBox
					label="Write Groups"
					placeholder="Select groups"
					bind:open={properties.writeGroups.combobox.open}
					bind:value={properties.writeGroups.combobox.value}
					data={properties.writeGroups.combobox.options}
					multiple={true}
					showCount={true}
					showCountLabel=" groups"
					width="w-2/5"
				/>
			{:else}
				<div class="flex items-center justify-between gap-2 rounded border p-2 md:col-span-2">
					<Label for="guest-writeable">Guest Writeable</Label>
					<Checkbox id="guest-writeable" bind:checked={properties.guest.writeable} />
				</div>
			{/if}

			<CustomValueInput
				label="Create Mask"
				placeholder="0664"
				bind:value={properties.createMask}
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label="Directory Mask"
				placeholder="2775"
				bind:value={properties.directoryMask}
				classes="flex-1 space-y-1.5"
			/>

			{#if appleExtensions}
				<div class="flex items-center justify-between gap-2 rounded border p-2 md:col-span-2">
					<Label for="time-machine">Time Machine</Label>
					<Checkbox id="time-machine" bind:checked={properties.timeMachine} />
				</div>

				{#if properties.timeMachine}
					<CustomValueInput
						label="Time Machine Max Size (GB)"
						placeholder="0"
						bind:value={properties.timeMachineMaxSize}
						classes="flex-1 space-y-1.5 md:col-span-2"
					/>
				{/if}
			{/if}
		</div>

		<div class="mt-4 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button onclick={createOrEdit}>{edit ? 'Save' : 'Create'}</Button>
		</div>
	</Dialog.Content>
</Dialog.Root>
