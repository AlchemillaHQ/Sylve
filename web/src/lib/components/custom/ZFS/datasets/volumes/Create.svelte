<script lang="ts">
	import { createVolume } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { GroupedByPool } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidSize } from '$lib/utils/numbers';
	import { generatePassword } from '$lib/utils/string';
	import { isValidDatasetName } from '$lib/utils/zfs';
	import { createVolProps } from '$lib/utils/zfs/dataset/volume';
	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		pools: Zpool[];
		grouped: GroupedByPool[];
		reload?: boolean;
	}

	let { open = $bindable(), pools, grouped, reload = $bindable() }: Props = $props();
	let options = {
		name: '',
		parent: '',
		checksum: 'on',
		compression: 'on',
		dedup: 'off',
		encryption: 'off',
		encryptionKey: '',
		volblocksize: '16384',
		size: '',
		primarycache: 'metadata',
		volmode: 'dev'
	};

	let properties = $state(options);
	type props = {
		checksum: string;
		compression: string;
		dedup: string;
		encryption: string;
		volblocksize: string;
		primarycache: string;
		volmode: string;
	};

	let zfsProperties = $state(createVolProps);

	async function create() {
		if (!isValidDatasetName(properties.name)) {
			toast.error('Invalid volume name', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.parent) {
			toast.error('Please select a pool', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.encryption !== 'off') {
			if (properties.encryptionKey === '') {
				toast.error('Encryption key is required', {
					position: 'bottom-center'
				});
				return;
			}
		}

		if (!isValidSize(properties.size)) {
			toast.error('Invalid volume size', {
				position: 'bottom-center'
			});
			return;
		}

		const parentSize = grouped.find((group) => group.pool.name === properties.parent)?.pool.free;

		if (!parentSize) {
			toast.error('Parent not found', {
				position: 'bottom-center'
			});
			return;
		}

		if (humanFormat.parse(properties.size) > parentSize) {
			toast.error('Volume size is greater than available space', {
				position: 'bottom-center'
			});
		}

		const response = await createVolume(properties.name, properties.parent, {
			parent: properties.parent,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			encryption: properties.encryption,
			encryptionKey: properties.encryptionKey,
			volblocksize: properties.volblocksize,
			size: properties.size,
			primarycache: properties.primarycache,
			volmode: properties.volmode
		});

		reload = true;

		if (response.error) {
			toast.error('Failed to create volume', {
				position: 'bottom-center'
			});
			handleAPIError(response);
			return;
		}

		let n = `${properties.parent}/${properties.name}`;
		toast.success(`Volume ${n} created`, {
			position: 'bottom-center'
		});

		open = false;
		properties = options;
	}
</script>

{#snippet simpleSelect(prop: keyof props, label: string, placeholder: string)}
	<SimpleSelect
		{label}
		{placeholder}
		options={zfsProperties[prop]}
		bind:value={properties[prop]}
		onChange={(value) => (properties[prop] = value)}
	/>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-4xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<div class="flex items-center">
						<span class="icon-[carbon--volume-block-storage] mr-2 h-5 w-5"></span>

						<span>Create Volume</span>
					</div>
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
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<div class="space-y-1">
					<Label class="w-24 whitespace-nowrap text-sm">Name</Label>
					<Input
						type="text"
						id="name"
						placeholder="firewall-vm-vol"
						autocomplete="off"
						bind:value={properties.name}
					/>
				</div>

				<div class="space-y-1">
					<Label class="w-24 whitespace-nowrap text-sm">Size</Label>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						bind:value={properties.size}
						placeholder="128M"
					/>
				</div>

				<SimpleSelect
					label="Pool"
					placeholder="Select Pool"
					options={pools.map((pool) => ({
						value: pool.name,
						label: pool.name
					}))}
					bind:value={properties.parent}
					onChange={(value) => (properties.parent = value)}
				/>

				{@render simpleSelect('volblocksize', 'Block Size', 'Select block size')}
				{@render simpleSelect('checksum', 'Checksum', 'Select Checksum')}
				{@render simpleSelect('compression', 'Compression', 'Select compression type')}
				{@render simpleSelect('dedup', 'Deduplication', 'Select deduplication mode')}
				{@render simpleSelect('encryption', 'Encryption', 'Select encryption')}

				{#if properties.encryption !== 'off'}
					<div class="space-y-1">
						<Label class="w-24 whitespace-nowrap text-sm">Passphrase</Label>
						<div class="flex w-full max-w-sm items-center space-x-2">
							<Input
								type="password"
								id="d-passphrase"
								placeholder="Enter or generate passphrase"
								class="w-full"
								autocomplete="off"
								bind:value={properties.encryptionKey}
								showPasswordOnFocus={true}
							/>

							<Button
								onclick={() => {
									properties.encryptionKey = generatePassword();
								}}
							>
								<span
									class="icon-[fad--random-2dice] h-6 w-6"
									onclick={() => {
										properties.encryptionKey = generatePassword();
									}}
								></span>
							</Button>
						</div>
					</div>
				{/if}

				{@render simpleSelect('primarycache', 'Primary Cache', 'Select primary cache mode')}
				{@render simpleSelect('volmode', 'Volume Mode', 'Select volume mode')}
			</div>
		</div>

		<Dialog.Footer>
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28 "
					onclick={() => {
						create();
					}}
				>
					Create
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
