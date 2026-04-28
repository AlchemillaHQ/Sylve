<script lang="ts">
	import { createVolume } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { GroupedByPool } from '$lib/types/zfs/dataset';
	import {
		normalizeSizeInputExact,
		parseSizeInputToBytes,
		toZfsBytesString
	} from '$lib/utils/bytes';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generatePassword } from '$lib/utils/string';
	import { isValidDatasetName } from '$lib/utils/zfs';
	import { createVolProps } from '$lib/utils/zfs/dataset/volume';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		grouped: GroupedByPool[];
		reload?: boolean;
	}

	let { open = $bindable(), grouped, reload = $bindable() }: Props = $props();

	const pools = resource(
		() => 'zfs-pools',
		async () => {
			const result = await getPools();
			updateCache('zfs-pools', result);
			return result;
		},
		{
			initialValue: []
		}
	);

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
	let volblocksizeOpen = $state(false);

	const volblocksizeData = $derived.by(() => {
		const base = zfsProperties.volblocksize;
		const val = properties.volblocksize;
		if (!val || base.some((d) => d.value === val)) return base;
		const humanized = normalizeSizeInputExact(val);
		const label = humanized ? `${humanized} - Custom` : `${val} - Custom`;
		return [{ value: val, label }, ...base];
	});

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

		const parsedSize = parseSizeInputToBytes(properties.size);
		if (parsedSize === null) {
			toast.error('Invalid volume size', {
				position: 'bottom-center'
			});
			return;
		}

		const parentPool = pools.current.find((pool) => pool.name === properties.parent);
		if (!parentPool) {
			toast.error('Parent pool not found', {
				position: 'bottom-center'
			});
			return;
		}
		const parentSize = Number(parentPool.free || 0);

		if (parsedSize > parentSize) {
			toast.error('Volume size is greater than available space', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await createVolume(properties.name, properties.parent, {
			parent: properties.parent,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			encryption: properties.encryption,
			encryptionKey: properties.encryptionKey,
			volblocksize: properties.volblocksize,
			size: toZfsBytesString(parsedSize),
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
		classes={{
			parent: 'flex-1 min-w-0 space-y-1',
			label: 'flex h-7 items-center whitespace-nowrap text-sm',
			trigger:
				'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
		}}
	/>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-4xl"
		showCloseButton={true}
		showResetButton={true}
		onReset={() => {
			properties = options;
		}}
		onClose={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[carbon--volume-block-storage]"
					size="h-5 w-5"
					gap="gap-2"
					title="Create Volume"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 w-full">
			<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
				<input type="text" style="display:none;" name="dummy_username" />
				<input type="password" style="display:none;" name="dummy_password" />

				<div class="space-y-1">
					<Label class="flex h-7 items-center whitespace-nowrap text-sm">Name</Label>
					<Input
						type="text"
						id="name"
						placeholder="firewall-vm-vol"
						autocomplete="off"
						bind:value={properties.name}
					/>
				</div>

				<div class="space-y-1">
					<Label class="flex h-7 items-center whitespace-nowrap text-sm">Size</Label>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						bind:value={properties.size}
						placeholder="128M"
						onblur={() => {
							const normalized = normalizeSizeInputExact(properties.size);
							if (normalized !== null) {
								properties.size = normalized;
							}
						}}
					/>
				</div>

				<SimpleSelect
					label="Pool"
					placeholder="Select Pool"
					options={pools.current.map((pool) => ({
						value: pool.name,
						label: pool.name
					}))}
					bind:value={properties.parent}
					onChange={(value) => (properties.parent = value)}
					classes={{
						parent: 'flex-1 min-w-0 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<CustomComboBox
					bind:open={volblocksizeOpen}
					label="Block Size"
					bind:value={properties.volblocksize}
					data={volblocksizeData}
					classes="space-y-1"
					placeholder="Select block size"
					triggerWidth="w-full"
					width="w-full"
					allowCustom={true}
				/>
				{@render simpleSelect('checksum', 'Checksum', 'Select Checksum')}
				{@render simpleSelect('compression', 'Compression', 'Select compression type')}
				{@render simpleSelect('dedup', 'Deduplication', 'Select deduplication mode')}
				{@render simpleSelect('encryption', 'Encryption', 'Select encryption')}

				{#if properties.encryption !== 'off'}
					<div class="space-y-1">
						<Label class="flex h-7 items-center whitespace-nowrap text-sm">Passphrase</Label>
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
									role="button"
									tabindex="0"
									class="icon-[fad--random-2dice] h-6 w-6"
									onclick={() => {
										properties.encryptionKey = generatePassword();
									}}
									onkeydown={(e) => {
										if (e.key === 'Enter' || e.key === ' ') {
											properties.encryptionKey = generatePassword();
										}
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
