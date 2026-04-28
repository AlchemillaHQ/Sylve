<script lang="ts">
	import { modifyExtraBhyveOptions } from '$lib/api/vm/vm';
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

	function currentAsText(): string {
		return (vm.extraBhyveOptions || []).join('\n');
	}

	let extraBhyveOptionsText = $state(currentAsText());

	function toOptionLines(raw: string): string[] {
		return raw
			.split('\n')
			.map((line) => line.trim())
			.filter((line) => line.length > 0);
	}

	async function modify() {
		if (!vm) return;

		const response = await modifyExtraBhyveOptions(vm.rid, toOptionLines(extraBhyveOptionsText));
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify extra bhyve options', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified extra bhyve options', {
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
			extraBhyveOptionsText = currentAsText();
		}}
		onClose={() => {
			extraBhyveOptionsText = currentAsText();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[material-symbols--terminal-rounded]"
					size="h-5 w-5"
					gap="gap-2"
					title="Extra Bhyve Options"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			placeholder="-S"
			bind:value={extraBhyveOptionsText}
			classes="flex-1 space-y-1.5"
			type="textarea"
			textAreaClasses="h-40 font-mono text-xs"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
