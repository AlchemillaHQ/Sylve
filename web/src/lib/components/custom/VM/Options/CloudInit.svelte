<script lang="ts">
	import { modifyCloudInitData } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), vm, reload = $bindable(false) }: Props = $props();
	let cloudInit = $state({
		data: vm.cloudInitData ?? '',
		metadata: vm.cloudInitMetaData ?? '',
		networkConfig: vm.cloudInitNetworkConfig ?? ''
	});

	async function modify() {
		if (!vm) return;
		if (cloudInit.data === '' && cloudInit.metadata === '') {
			// both are empty, proceed
		} else if (cloudInit.data === '' || cloudInit.metadata === '') {
			toast.error('Either both user and meta data should be empty or both should be provided', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await modifyCloudInitData(
			vm.rid,
			cloudInit.data,
			cloudInit.metadata,
			cloudInit.networkConfig
		);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify Cloud Init data', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified Cloud Init data', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[90vh] flex-col overflow-hidden p-5"
		showResetButton={true}
		onReset={() => {
			cloudInit.data = vm.cloudInitData ?? '';
			cloudInit.metadata = vm.cloudInitMetaData ?? '';
			cloudInit.networkConfig = vm.cloudInitNetworkConfig ?? '';
		}}
		onClose={() => {
			cloudInit.data = vm.cloudInitData ?? '';
			cloudInit.metadata = vm.cloudInitMetaData ?? '';
			cloudInit.networkConfig = vm.cloudInitNetworkConfig ?? '';
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[fluent--cloud-cube-24-regular]"
					size="h-5 w-5"
					gap="gap-2"
					title="Cloud Init"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label={'User Data'}
			placeholder="Cloud Init Data"
			bind:value={cloudInit.data}
			classes="flex-1 space-y-1.5 mb-4"
			type="textarea"
			textAreaClasses="h-32"
		/>

		<CustomValueInput
			label={'Metadata'}
			placeholder="Cloud Init Metadata"
			bind:value={cloudInit.metadata}
			classes="flex-1 space-y-1.5"
			type="textarea"
			textAreaClasses="h-32"
		/>

		<CustomValueInput
			label={'Network Config'}
			placeholder="Cloud Init Network Config"
			bind:value={cloudInit.networkConfig}
			classes="flex-1 space-y-1.5"
			type="textarea"
			textAreaClasses="h-32"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
