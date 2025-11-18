<script lang="ts">
	import { createDirStorage, createS3Storage } from '$lib/api/cluster/storage';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterStorages } from '$lib/types/cluster/storage';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		storages: ClusterStorages;
	}

	let { open = $bindable(), reload = $bindable(), storages }: Props = $props();
	let options = {
		s3: {
			name: '',
			endpoint: '',
			region: '',
			bucket: '',
			accessKey: '',
			secretKey: ''
		},
		dir: {
			name: '',
			path: ''
		}
	};

	let properties = $state(options);
	let loading = $state(false);

	let type = $state({
		combobox: {
			open: false,
			value: '' as '' | 's3' | 'dir'
		}
	});

	async function create() {
		if (type.combobox.value === 's3') {
			const data = properties.s3;
			if (
				!data.name ||
				!data.endpoint ||
				!data.region ||
				!data.bucket ||
				!data.accessKey ||
				!data.secretKey
			) {
				toast.error('Missing required fields', {
					position: 'bottom-center'
				});
				return;
			}

			loading = true;

			const response = await createS3Storage(
				data.name,
				data.endpoint,
				data.region,
				data.bucket,
				data.accessKey,
				data.secretKey
			);

			loading = false;
			reload = true;

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to create S3 storage', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success('S3 storage created', {
				position: 'bottom-center'
			});

			open = false;
		} else if (type.combobox.value === 'dir') {
			const data = properties.dir;
			if (!data.name || !data.path) {
				toast.error('Missing required fields', {
					position: 'bottom-center'
				});
				return;
			}

			loading = true;

			const response = await createDirStorage(data.name, data.path);
			loading = false;
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to create Directory storage', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success('Directory storage created', {
				position: 'bottom-center'
			});
			open = false;
		} else {
			toast.error('Please select a storage type', {
				position: 'bottom-center'
			});
			return;
		}
	}
</script>

{#snippet s3Input(name: string, label: string)}
	<CustomValueInput
		bind:value={properties.s3[name as keyof typeof properties.s3]}
		placeholder={label}
		classes="flex-1 space-y-1.5"
	/>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--storage] h-6 w-6"></span>
					<span>Create Storage</span>
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

		<CustomComboBox
			bind:open={type.combobox.open}
			label="Type"
			bind:value={type.combobox.value}
			data={[
				{ value: 's3', label: 'S3' },
				{ value: 'dir', label: 'Directory' }
			]}
			classes="flex-1 space-y-1"
			placeholder="Select Type"
			triggerWidth="w-full"
			width="w-full lg:w-[75%]"
		></CustomComboBox>

		{#if type.combobox.value === 's3'}
			<div class="mt-0 grid grid-cols-2 gap-4">
				{@render s3Input('name', 'Name')}
				{@render s3Input('endpoint', 'Endpoint')}
				{@render s3Input('region', 'Region')}
				{@render s3Input('bucket', 'Bucket')}
				{@render s3Input('accessKey', 'Access Key')}
				{@render s3Input('secretKey', 'Secret Key')}
			</div>
		{/if}

		{#if type.combobox.value === 'dir'}
			<div class="grid grid-cols-2 gap-4">
				<CustomValueInput
					bind:value={properties.dir.name}
					label="Name"
					placeholder="Backups"
					classes="flex-1 space-y-1.5"
				/>
				<CustomValueInput
					bind:value={properties.dir.path}
					label="Path"
					placeholder="/var/lib/sylve/backups"
					classes="flex-1 space-y-1.5"
				/>
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={create} type="submit" size="sm" disabled={loading}>
					{#if loading}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
