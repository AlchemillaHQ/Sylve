<script lang="ts">
	import { modifyClockOffset } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		node: string;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), node, vm, reload = $bindable(false) }: Props = $props();

	let comboBox = $state({
		open: false,
		value: untrack(() => (vm.timeOffset === 'utc' ? 'utc' : 'localtime')),
		options: [
			{
				label: 'UTC',
				value: 'utc'
			},
			{
				label: 'Local Time',
				value: 'localtime'
			}
		]
	});
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyClockOffset(vm.rid, comboBox.value as 'localtime' | 'utc', {
				hostname: node
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify clock offset', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No clock-offset changes needed'
					: 'Modified clock offset',
				{ position: 'bottom-center' }
			);

			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			comboBox.value = vm.timeOffset === 'utc' ? 'utc' : 'localtime';
		}}
		onClose={() => {
			comboBox.value = vm.timeOffset === 'utc' ? 'utc' : 'localtime';
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--clock]" size="h-5 w-5" gap="gap-2" title="Clock Offset" />
			</Dialog.Title>
		</Dialog.Header>

		<ComboBox
			bind:open={comboBox.open}
			label="Offset"
			bind:value={comboBox.value}
			data={comboBox.options}
			classes="flex-1 space-y-1"
			placeholder="Select type"
			width="w-3/4"
			disabled={saving}
		></ComboBox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
