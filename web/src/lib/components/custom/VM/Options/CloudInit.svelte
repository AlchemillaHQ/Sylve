<script lang="ts">
	import { modifyCloudInitData } from '$lib/api/vm/vm';
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
		metadata: vm.cloudInitMetaData ?? ''
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

		const response = await modifyCloudInitData(vm.rid, cloudInit.data, cloudInit.metadata);
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
	<Dialog.Content class="w-1/2 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[fluent--cloud-cube-24-regular] h-5 w-5"></span>

					<span>Cloud Init</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							cloudInit.data = vm.cloudInitData ?? '';
							cloudInit.metadata = vm.cloudInitMetaData ?? '';
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
							cloudInit.data = vm.cloudInitData ?? '';
							cloudInit.metadata = vm.cloudInitMetaData ?? '';
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
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

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
