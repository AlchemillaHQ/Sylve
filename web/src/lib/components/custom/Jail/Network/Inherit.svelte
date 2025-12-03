<script lang="ts">
	import type { Jail } from '$lib/types/jail/jail';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { setNetworkInheritance } from '$lib/api/jail/jail';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), jail = $bindable(), reload = $bindable() }: Props = $props();

	let inherited = $derived.by(() => {
		if (jail) {
			console.log(jail.inheritIPv4, jail.inheritIPv6);
			return jail.inheritIPv4 || jail.inheritIPv6;
		}

		return false;
	});

	let selected = $state({
		ipv4: jail.inheritIPv4,
		ipv6: jail.inheritIPv6
	});

	async function inherit() {
		if (!selected.ipv4 && !selected.ipv6) {
			toast.error('At least one protocol required', {
				position: 'bottom-center'
			});
			return;
		}

		const result = await setNetworkInheritance(jail.ctId, selected.ipv4, selected.ipv6);
		reload = true;
		if (result.status === 'success') {
			toast.success('Network inherited', {
				position: 'bottom-center'
			});

			open = false;
			return;
		} else {
			toast.error('Failed to inherit network', {
				position: 'bottom-center'
			});
		}
	}

	async function disinherit() {
		const result = await setNetworkInheritance(jail.ctId, false, false);
		reload = true;
		if (result.status === 'success') {
			toast.success('Network disinherited', {
				position: 'bottom-center'
			});

			open = false;
			return;
		} else {
			toast.error('Failed to disinherit network', {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>
					{#if inherited}
						Disinherit Network
					{:else}
						Inherit Network
					{/if}
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if inherited}
			<span class="text-muted-foreground text-justify text-sm">
				This option will disinherit the network configuration from the host. Choose this if you want
				to attach a custom network switch to this jail or disable networking entirely. Changes will
				take effect after restarting the jail.
			</span>
		{:else}
			<span class="text-muted-foreground text-justify text-sm">
				This option will inherit the network configuration from the host. Choose this if you want
				the jail to share the host's networking. You can select which protocols to inherit below.
				Changes will take effect after restarting the jail.
			</span>

			<div>
				<div class="flex flex-row gap-4">
					<CustomCheckbox
						label="IPv4"
						bind:checked={selected.ipv4}
						classes="flex items-center gap-2"
					></CustomCheckbox>
					<CustomCheckbox
						label="IPv6"
						bind:checked={selected.ipv6}
						classes="flex items-center gap-2"
					></CustomCheckbox>
				</div>
			</div>
		{/if}

		<Dialog.Footer class="-mt-4 flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !inherited}
					<Button onclick={inherit} type="submit" size="sm">Save</Button>
				{:else}
					<Button onclick={disinherit} type="submit" size="sm">Disinherit</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
