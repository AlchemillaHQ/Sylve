<script lang="ts">
	import { modifyBootRom } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
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
	<Dialog.Content
		class="w-1/3 overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			comboBox.value = vm.bootRom;
		}}
		onClose={() => {
			comboBox.value = vm.bootRom;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--chip]" size="h-5 w-5" gap="gap-2" title="Boot ROM" />
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
