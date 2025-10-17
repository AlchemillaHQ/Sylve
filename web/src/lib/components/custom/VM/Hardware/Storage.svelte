<script lang="ts">
	import { getFiles } from '$lib/api/system/file-explorer';
	import { storageAttach } from '$lib/api/vm/storage';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import Icon from '@iconify/svelte';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
	}

	let { open = $bindable(), datasets, downloads, vm }: Props = $props();

	let options = {
		type: '',
		name: '',
		dataset: '',
		size: '',
		emulation: 'ahci-hd'
	};

	let properties = $state(options);
	let isos = $derived(getISOs(downloads, true));
	let usedVolumes = $derived.by(() => {
		const storages = vm.storages;
		return datasets
			.filter((dataset) => dataset.type === 'volume')
			.map((dataset) => ({
				name: dataset.name,
				guid: dataset.guid
			}))
			.filter((dataset) => {
				return storages.some((storage) => {
					return storage.dataset === dataset.guid;
				});
			});
	});

	let existingImage = $state(false);

	$effect(() => {
		if (properties.name && properties.type === 'raw' && properties.dataset) {
			const dataset = datasets.find(
				(d) => d.guid === properties.dataset || d.name === properties.dataset
			);
			const mountPoint = dataset?.mountpoint || '';
			if (mountPoint) {
				getFiles(mountPoint).then((files) => {
					for (const file of files) {
						if (file.id === `${mountPoint}/${properties.name}.img`) {
							existingImage = true;
							properties.size = humanFormat(file.size || 0);
							return;
						}
					}

					existingImage = false;
				});
			}
		}
	});

	async function attach() {
		if (!properties.type || !properties.dataset) {
			toast.error('Please select a type and dataset', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.type === 'iso') {
			const response = await storageAttach(vm.vmId, 'iso', properties.dataset, properties.emulation, 0, '');
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to attach CD-ROM', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success('CD-ROM attached', {
					position: 'bottom-center'
				});

				properties = options;
				open = false;
			}
		}

		if (properties.type === 'raw' || properties.type === 'zvol') {
			if (!properties.emulation) {
				toast.error('Please select an emulation type', {
					position: 'bottom-center'
				});
				return;
			}

			if (!properties.dataset) {
				let type = properties.type === 'raw' ? 'ZFS Filesystem' : 'ZFS Volume';
				toast.error(`Please select a ${type}`, {
					position: 'bottom-center'
				});
				return;
			}
		}

		if (properties.type === 'zvol') {
			const response = await storageAttach(
				vm.vmId,
				'zvol',
				properties.dataset,
				properties.emulation,
				0,
				''
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to attach ZFS Volume', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success('ZFS Volume attached', {
					position: 'bottom-center'
				});

				properties = options;
				open = false;
			}
		}

		if (properties.type === 'raw') {
			if (!properties.name || !properties.size) {
				toast.error('Name and size required', {
					position: 'bottom-center'
				});
				return;
			}

			let parsedSize = 0;

			try {
				parsedSize = humanFormat.parse(properties.size);
			} catch (e) {
				parsedSize = 0;
			}

			if (parsedSize <= 0) {
				toast.error('Invalid size', {
					position: 'bottom-center'
				});
				return;
			}

			if (!/^[a-zA-Z0-9-_]+$/.test(properties.name)) {
				toast.error('Invalid name', {
					position: 'bottom-center'
				});
				return;
			}

			const response = await storageAttach(
				vm.vmId,
				properties.type,
				properties.dataset,
				properties.emulation,
				Math.floor(parsedSize),
				properties.name
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to attach storage', {
					position: 'bottom-center'
				});
				return;
			} else {
				toast.success('Storage attached', {
					position: 'bottom-center'
				});
				properties = options;
				open = false;
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-md overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<Icon icon="grommet-icons:storage" class="h-5 w-5" />
					<span>New Storage</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4"
						onclick={() => {
							properties = options;
						}}
					>
						<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<SimpleSelect
			label="Type"
			placeholder="Select Type"
			options={[
				{ value: 'iso', label: 'CD-ROM' },
				{ value: 'raw', label: 'Disk' },
				{ value: 'zvol', label: 'ZFS Volume' }
			]}
			bind:value={properties.type}
			onChange={(value) => (properties.type = value)}
		/>

        {#if properties.type === 'iso'}
            <div class="flex flex-row gap-2">
                <div class="flex-1 min-w-[0]">
                    <div class="w-full max-w-[300px]">
                        <SimpleSelect
                            label="ISO / Image"
                            placeholder="Select ISO or Image"
                            options={isos}
                            bind:value={properties.dataset}
                            onChange={(value) => (properties.dataset = value)}
                        />
                    </div>
                </div>

                <div class="flex-1 min-w-[0]">
                    <div class="w-full max-w-[200px]">
                        <SimpleSelect
                            label="Emulation"
                            placeholder="Select Emulation"
                            options={[
                                { value: 'ahci-cd', label: 'AHCI CD' },
                                { value: 'ahci-hd', label: 'AHCI HD' },
                                { value: 'virtio-blk', label: 'VirtIO Block' },
                                { value: 'nvme', label: 'NVMe' }
                            ]}
                            bind:value={properties.emulation}
                            onChange={(value) => (properties.emulation = value)}
                        />
                    </div>
                </div>
            </div>
        {/if}


		{#if properties.type === 'zvol'}
			<SimpleSelect
				label="ZFS Volume"
				placeholder="Select ZFS Volume"
				options={datasets
					.filter((dataset) => {
						return (
							dataset.type === 'volume' && !usedVolumes.some((used) => used.guid === dataset.guid)
						);
					})
					.map((dataset) => ({
						value: dataset.guid || dataset.name,
						label: dataset.name
					}))}
				bind:value={properties.dataset}
				onChange={(value) => (properties.dataset = value)}
			/>
		{/if}

		{#if properties.type === 'raw'}
			<SimpleSelect
				label="ZFS Filesystem"
				placeholder="Select ZFS Filesystem"
				options={datasets
					.filter((dataset) => {
						return (
							dataset.type === 'filesystem' &&
							!usedVolumes.some((used) => used.guid === dataset.guid)
						);
					})
					.map((dataset) => ({
						value: dataset.guid || dataset.name,
						label: dataset.name
					}))}
				bind:value={properties.dataset}
				onChange={(value) => (properties.dataset = value)}
			/>

			<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<CustomValueInput
					label="Name"
					placeholder="raw-disk-1"
					bind:value={properties.name}
					classes="flex-1 space-y-1"
				/>

				<CustomValueInput
					label="Size"
					placeholder="8 GB"
					bind:value={properties.size}
					classes="flex-1 space-y-1"
					disabled={existingImage}
				/>

				{#if existingImage}
					<span class="-mt-3 text-xs text-yellow-500">Existing image will be used</span>
				{/if}
			</div>
		{/if}

		{#if properties.type === 'zvol' || properties.type === 'raw'}
			<SimpleSelect
				label="Emulation"
				placeholder="Select Emulation"
				options={[
					{ value: 'ahci-hd', label: 'AHCI HD' },
					{ value: 'virtio-blk', label: 'VirtIO Block' },
					{ value: 'nvme', label: 'NVMe' }
				]}
				bind:value={properties.emulation}
				onChange={(value) => (properties.emulation = value)}
			/>
		{/if}

		<Dialog.Footer>
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28 "
					onclick={() => {
						attach();
					}}
				>
					Attach
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
