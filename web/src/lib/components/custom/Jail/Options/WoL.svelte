<script lang="ts">
	import { modifyWoL } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), jail, reload = $bindable(false) }: Props = $props();
	let wol = $state(jail.wol);

	async function modify() {
		if (!jail) return;
		const response = await modifyWoL(jail.ctId, wol);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify WoL setting', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified WoL setting', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			wol = jail.wol;
		}}
		onClose={() => {
			wol = jail.wol;
			open = false;
		}}
	>
		<Dialog.Header class="">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[arcticons--wakeonlan]"
					size="h-5 w-5"
					gap="gap-2"
					title="Wake on LAN"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<span class="text-muted-foreground text-justify text-sm">
			Setting this option to be <b>on</b> will enable Wake on LAN for this jail for all MAC addresses
			attached to it
		</span>
		<CustomCheckbox label="WoL" bind:checked={wol} classes="flex items-center gap-2"
		></CustomCheckbox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
