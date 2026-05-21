<script lang="ts">
	import { flashVolume } from '$lib/api/zfs/datasets';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { sleep } from '$lib/utils';
	import { handleAPIError } from '$lib/utils/http';
	import { getISOs } from '$lib/utils/utilities/downloader';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		dataset: Dataset;
		downloads: Download[];
		reload?: boolean;
	}

	let { open = $bindable(), dataset, downloads, reload = $bindable() }: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		select: {
			open: false,
			uuid: '',
			data: getISOs(downloads, true)
		},
		loading: false
	};

	let properties = $state(options);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="p-5"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		onClose={() => {
			properties = options;
			open = false;
		}}
		showResetButton={true}
		onReset={() => {
			properties = options;
		}}
	>
		<Dialog.Header class="p-0 -mb-2">
			<Dialog.Title class="flex justify-between">
				<SpanWithIcon
					icon="icon-[mdi--usb-flash-drive-outline]"
					size="w-6 h-6"
					gap="gap-2"
					title="Flash File To Volume"
				/>
			</Dialog.Title>
			<Dialog.Description>
				{dataset.name}
			</Dialog.Description>
		</Dialog.Header>

		<div class="min-w-0 flex-1 space-y-0 overflow-hidden">
			<CustomComboBox
				bind:open={properties.select.open}
				label="Select File"
				bind:value={properties.select.uuid}
				data={properties.select.data}
				classes="flex-1 space-y-1.5"
				placeholder="File"
				triggerWidth="w-full"
				width="w-full"
				shortLabels={true}
			></CustomComboBox>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2 py-2">
				<Button
					onclick={async () => {
						properties.loading = true;
						await sleep(1000);

						const response = await flashVolume(dataset.guid || '', properties.select.uuid);

						reload = true;

						if (response.status === 'error') {
							handleAPIError(response);
							toast.error('Error flashing volume', {
								position: 'bottom-center'
							});
						} else {
							toast.success(`${'Volume ' + dataset.name + ' flashed'}`, {
								position: 'bottom-center'
							});
						}

						properties = options;
						open = false;
					}}
					type="submit"
					size="sm"
					class="w-full lg:w-28"
					disabled={!properties.select.uuid || properties.loading}
				>
					{#if properties.loading}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						<span>Flash</span>
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
