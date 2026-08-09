<script lang="ts">
	import { modifyExtraBhyveOptions } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
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

	function currentAsText(): string {
		return (vm.extraBhyveOptions || []).join('\n');
	}

	let extraBhyveOptionsText = $state(untrack(currentAsText));
	let saving = $state(false);

	function toOptionLines(raw: string): string[] {
		return raw
			.split('\n')
			.map((line) => line.trim())
			.filter((line) => line.length > 0);
	}

	async function modify() {
		if (saving) return;
		const options = toOptionLines(extraBhyveOptionsText);
		const totalBytes = options.reduce((total, option) => total + option.length, 0);
		if (
			options.length > 128 ||
			options.some((option) => option.length > 4096) ||
			totalBytes > 65536
		) {
			toast.error('Extra bhyve options exceed the supported size', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyExtraBhyveOptions(vm.rid, options, { hostname: node });
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify extra bhyve options', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No extra bhyve option changes needed'
					: 'Modified extra bhyve options',
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
			disabled={saving}
		/>

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
