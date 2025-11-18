<script lang="ts">
	import { createFileSystem } from '$lib/api/zfs/datasets';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { Dataset, GroupedByPool } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidSize } from '$lib/utils/numbers';
	import { generatePassword } from '$lib/utils/string';
	import { isValidDatasetName } from '$lib/utils/zfs';
	import { createFSProps } from '$lib/utils/zfs/dataset/fs';
	import type { ParsedInfo, ScaleLike } from 'human-format';
	import humanFormat from 'human-format';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		datasets: Dataset[];
		grouped: GroupedByPool[];
		reload?: boolean;
	}

	let { open = $bindable(), datasets, grouped, reload = $bindable() }: Props = $props();
	let parents = $derived.by(() => {
		let options = [] as { label: string; value: string }[];
		for (const pool of grouped) {
			for (const fs of pool.filesystems) {
				options.push({
					label: fs.name,
					value: fs.name
				});
			}
		}

		return options;
	});

	let options = {
		name: '',
		parent: {
			open: false,
			value: ''
		},
		atime: 'on',
		checksum: 'on',
		compression: 'on',
		dedup: 'off',
		encryption: 'off',
		encryptionKey: '',
		quota: '',
		aclinherit: 'passthrough',
		aclmode: 'passthrough',
		recordsize: '131072'
	};

	let zfsProperties = $state(createFSProps);
	let properties = $state(options);

	let remainingSpace = $state(0);
	let currentPartition = $state(0);
	let currentPartitionInput = $derived(properties.quota);

	$effect(() => {
		if (currentPartitionInput === '') {
			currentPartition = 0;
		} else {
			let parsed: ParsedInfo<ScaleLike> | null = null;

			try {
				parsed = humanFormat.parse.raw(currentPartitionInput);
			} catch (e) {
				parsed = { factor: 1, value: 0, prefix: 'B' };
				currentPartitionInput = '1B';
			}

			if (parsed) {
				untrack(() => {
					currentPartition = parsed.factor * parsed.value;
					if (currentPartition > remainingSpace) {
						currentPartition = remainingSpace;
						currentPartitionInput = humanFormat(remainingSpace);
					}
				});
			}
		}
	});

	async function create() {
		if (!isValidDatasetName(properties.name)) {
			toast.error('Invalid name', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.parent.value) {
			toast.error('No parent selected', {
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

		if (properties.quota !== '') {
			if (!isValidSize(properties.quota)) {
				toast.error('Invalid quota size', {
					position: 'bottom-center'
				});
				return;
			}
		}

		const response = await createFileSystem(properties.name, properties.parent.value, {
			parent: properties.parent.value,
			atime: properties.atime,
			checksum: properties.checksum,
			compression: properties.compression,
			dedup: properties.dedup,
			encryption: properties.encryption,
			encryptionKey: properties.encryptionKey,
			quota: properties.quota,
			aclinherit: properties.aclinherit,
			aclmode: properties.aclmode,
			recordsize: properties.recordsize
		});

		reload = true;

		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to create filesystem', {
				position: 'bottom-center'
			});
			return;
		}

		let n = `${properties.parent.value}/${properties.name}`;
		toast.success(`File System ${n} created`, {
			position: 'bottom-center'
		});

		properties = options;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed top-1/2 left-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-3xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--files] h-5 w-5"></span>

					Create Filesystem
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
				<div class="space-y-1.5">
					<Label for="name" class="w-24 text-sm whitespace-nowrap">Name</Label>
					<Input
						type="text"
						id="name"
						placeholder="simple-filesystem"
						autocomplete="off"
						bind:value={properties.name}
					/>
				</div>

				<CustomComboBox
					bind:open={properties.parent.open}
					label="Parent"
					bind:value={properties.parent.value}
					data={parents}
					classes="flex-1 space-y-1.5"
					placeholder="Select parent"
					triggerWidth="w-full"
					width="w-full"
				></CustomComboBox>

				<SimpleSelect
					label="ATime"
					placeholder="Select ATime"
					options={zfsProperties.atime}
					bind:value={properties.atime}
					onChange={(value) => (properties.atime = value)}
				/>

				<SimpleSelect
					label="Checksum"
					placeholder="Select Checksum"
					options={zfsProperties.checksum}
					bind:value={properties.checksum}
					onChange={(value) => (properties.checksum = value)}
				/>

				<SimpleSelect
					label="Compression"
					placeholder="Select Compression"
					options={zfsProperties.compression}
					bind:value={properties.compression}
					onChange={(value) => (properties.compression = value)}
				/>

				<SimpleSelect
					label="Deduplication"
					placeholder="Select Deduplication"
					options={zfsProperties.dedup}
					bind:value={properties.dedup}
					onChange={(value) => (properties.dedup = value)}
				/>

				<SimpleSelect
					label="Encryption"
					placeholder="Select Encryption"
					options={zfsProperties.encryption}
					bind:value={properties.encryption}
					onChange={(value) => (properties.encryption = value)}
				/>

				{#if properties.encryption !== 'off'}
					<div class="space-y-1">
						<Label class="w-24 text-sm whitespace-nowrap">Passphrase</Label>
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

				<div class="space-y-1">
					<Label class="w-24 text-sm whitespace-nowrap">Quota</Label>
					<Input
						type="text"
						class="w-full text-left"
						min="0"
						max={remainingSpace}
						bind:value={properties.quota}
						placeholder="256M (Empty for no quota)"
					/>
				</div>

				<SimpleSelect
					label="ACL Inherit"
					placeholder="Select ACL Inherit"
					options={zfsProperties.aclInherit}
					bind:value={properties.aclinherit}
					onChange={(value) => (properties.aclinherit = value)}
				/>

				<SimpleSelect
					label="ACL Mode"
					placeholder="Select ACL Mode"
					options={zfsProperties.aclMode}
					bind:value={properties.aclmode}
					onChange={(value) => (properties.aclmode = value)}
				/>

				<SimpleSelect
					label="Recordsize"
					placeholder="Select Recordsize"
					options={zfsProperties.recordsize}
					bind:value={properties.recordsize}
					onChange={(value) => (properties.recordsize = value)}
				/>
			</div>
		</div>

		<Dialog.Footer class="mt-4">
			<div class="flex items-center justify-end space-x-4">
				<Button
					size="sm"
					type="button"
					class="h-8 w-full lg:w-28"
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
