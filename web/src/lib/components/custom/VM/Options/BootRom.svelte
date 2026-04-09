<script lang="ts">
	import { modifyBootRom } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
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

	let comboBox = $state({
		open: false,
		value: vm.bootRom,
		options: [
			{
				label: 'UEFI (Default)',
				value: 'uefi'
			},
			{
				label: 'None',
				value: 'none'
			}
		]
	});

	async function modify() {
		if (!vm) return;
		const response = await modifyBootRom(vm.rid, comboBox.value as 'uefi' | 'none');
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify boot ROM', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified boot ROM', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-1/3 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--chip] h-5 w-5"></span>
					<span>Boot ROM</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							comboBox.value = vm.bootRom;
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
							comboBox.value = vm.bootRom;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<ComboBox
			bind:open={comboBox.open}
			label={'Boot ROM'}
			bind:value={comboBox.value}
			data={comboBox.options}
			classes="flex-1 space-y-1"
			placeholder="Select boot ROM"
			width="w-full"
		></ComboBox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
