<script lang="ts">
	import { createSambaShare, updateSambaShare } from '$lib/api/samba/share';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { Group } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { SambaShare } from '$lib/types/samba/shares';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
		share?: SambaShare | null;
		edit?: boolean;
		reload?: boolean;
	}

	let {
		open = $bindable(),
		shares,
		datasets,
		groups,
		share,
		edit = false,
		reload = $bindable()
	}: Props = $props();

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
							dataset.mountpoint !== '/' &&
							dataset.mounted
					)
					.map((dataset) => ({
						label: dataset.name,
						value: dataset.guid ? dataset.guid : dataset.name
					}))
			}
		},
		readOnlyGroups: {
			combobox: {
				open: false,
				value: share ? share.readOnlyGroups.map((group) => group.name) : ([] as string[]),
				options: groups.map((group) => ({
					label: group.name,
					value: group.name
				}))
			}
		},
		writeableGroups: {
			combobox: {
				open: false,
				value: share ? share.writeableGroups.map((group) => group.name) : ([] as string[]),
				options: groups.map((group) => ({
					label: group.name,
					value: group.name
				}))
			}
		},
		createMask: share ? share.createMask : '0664',
		directoryMask: share ? share.directoryMask : '2775',
		guestOk: share ? share.guestOk : false,
		readOnly: share ? share.readOnly : false
	};

	let properties = $state(options);

	async function createOrEdit() {
		let error = '';

		if (shares.some((share) => share.name === properties.name) && share?.name !== properties.name) {
			error = 'Share name already exists';
		}

		if (properties.name === '') {
			error = 'Name is required';
		} else if (properties.dataset.combobox.value === '') {
			error = 'Dataset is required';
		} else if (
			properties.readOnlyGroups.combobox.value.length === 0 &&
			properties.writeableGroups.combobox.value.length === 0 &&
			!properties.guestOk
		) {
			error = 'No groups selected and guests are not allowed';
		}

		if (
			properties.readOnlyGroups.combobox.value.some((group) =>
				properties.writeableGroups.combobox.value.includes(group)
			)
		) {
			error = 'Share cannot have overlapping groups';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		let response: APIResponse;

		if (edit) {
			response = await updateSambaShare(
				share!.id,
				properties.name,
				properties.dataset.combobox.value,
				properties.readOnlyGroups.combobox.value,
				properties.writeableGroups.combobox.value,
				properties.createMask,
				properties.directoryMask,
				properties.guestOk,
				properties.readOnly
			);
		} else {
			response = await createSambaShare(
				properties.name,
				properties.dataset.combobox.value,
				properties.readOnlyGroups.combobox.value,
				properties.writeableGroups.combobox.value,
				properties.createMask,
				properties.directoryMask,
				properties.guestOk
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

	$effect(() => {
		if (properties.readOnly) {
			if (properties.readOnlyGroups.combobox.value.length > 0) {
				properties.readOnlyGroups.combobox.value = [];
			}
		}
	});
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
						title={'Reset'}
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
						title={'Close'}
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
				label={'Name'}
				placeholder="share"
				bind:value={properties.name}
				classes="flex-1 space-y-1.5"
			/>

			<CustomComboBox
				label={'Dataset'}
				placeholder="Select dataset"
				bind:open={properties.dataset.combobox.open}
				bind:value={properties.dataset.combobox.value}
				data={properties.dataset.combobox.options}
				multiple={false}
				width="w-full"
			/>
		</div>

		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomComboBox
				label={'Read-Only Groups'}
				placeholder="Select groups"
				bind:open={properties.readOnlyGroups.combobox.open}
				bind:value={properties.readOnlyGroups.combobox.value}
				data={properties.readOnlyGroups.combobox.options}
				disabled={properties.readOnly}
				multiple={true}
				width="w-full"
			/>

			<CustomComboBox
				label={'Writeable Groups'}
				placeholder="Select groups"
				bind:open={properties.writeableGroups.combobox.open}
				bind:value={properties.writeableGroups.combobox.value}
				data={properties.writeableGroups.combobox.options}
				multiple={true}
				width="w-full"
			/>
		</div>

		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomValueInput
				label={'Create Mask'}
				placeholder="0664"
				bind:value={properties.createMask}
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label={'Directory Mask'}
				placeholder="2775"
				bind:value={properties.directoryMask}
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="flex items-center space-x-4">
			<div class="flex items-center space-x-2">
				<Checkbox id="guests" bind:checked={properties.guestOk} />
				<Label for="guests" class="text-sm font-medium">Guests</Label>
			</div>

			<div class="flex items-center space-x-2">
				<Checkbox id="read-only" bind:checked={properties.readOnly} />
				<Label for="read-only" class="text-sm font-medium">Read Only</Label>
			</div>
		</div>

		<Dialog.Footer class="mt-4">
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28"
					onclick={() => {
						createOrEdit();
					}}
				>
					{#if edit}
						Edit
					{:else}
						Create
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
