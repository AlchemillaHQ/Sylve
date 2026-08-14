<script lang="ts">
	import { setNetworkInheritance } from '$lib/api/jail/jail';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		hostname: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), jail, hostname, onSaved }: Props = $props();
	const currentSelection = () => ({ ipv4: jail.inheritIPv4, ipv6: jail.inheritIPv6 });
	let selected = $state(currentSelection());
	let saving = $state(false);
	let removalConfirmationOpen = $state(false);
	let willRemoveNetworks = $derived(jail.networks.length > 0 && (selected.ipv4 || selected.ipv6));

	watch(
		() => `${jail.ctId}:${jail.inheritIPv4}:${jail.inheritIPv6}`,
		() => {
			selected = { ipv4: jail.inheritIPv4, ipv6: jail.inheritIPv6 };
		}
	);

	async function save() {
		if (saving) return;
		if (willRemoveNetworks && !removalConfirmationOpen) {
			removalConfirmationOpen = true;
			return;
		}

		const requestIdentity = `${hostname}:${jail.ctId}`;
		saving = true;
		try {
			const result = await setNetworkInheritance(jail.ctId, selected.ipv4, selected.ipv6, {
				hostname,
				preserveErrors: true
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error('Failed to update network inheritance', { position: 'bottom-center' });
				return;
			}
			const inherited = result.inheritIPv4 || result.inheritIPv6;
			toast.success(inherited ? 'Network inheritance updated' : 'Network inheritance disabled', {
				position: 'bottom-center',
				description:
					result.removedNetworkIds.length > 0
						? `${result.removedNetworkIds.length} custom network attachment(s) removed.`
						: undefined
			});
			if (`${hostname}:${jail.ctId}` === requestIdentity) {
				await onSaved();
				removalConfirmationOpen = false;
				open = false;
			}
		} catch {
			toast.error('Failed to update network inheritance', { position: 'bottom-center' });
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>
					Network Inheritance
				</div>
				<Button
					size="sm"
					variant="link"
					class="h-4"
					title="Close"
					disabled={saving}
					onclick={() => (open = false)}
				>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<p class="text-muted-foreground text-justify text-sm">
			Choose which host protocol stacks this jail inherits. Leave both disabled to use custom
			network attachments or to keep networking disabled.
		</p>
		<div class="flex flex-row gap-4">
			<CustomCheckbox label="IPv4" bind:checked={selected.ipv4} classes="flex items-center gap-2" />
			<CustomCheckbox label="IPv6" bind:checked={selected.ipv6} classes="flex items-center gap-2" />
		</div>
		{#if willRemoveNetworks}
			<p class="text-destructive text-sm">
				Enabling inheritance will permanently remove {jail.networks.length} custom network attachment(s).
			</p>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} type="submit" size="sm" disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

{#if removalConfirmationOpen}
	<AlertDialog
		bind:open={removalConfirmationOpen}
		customTitle={`Enabling inheritance will permanently remove ${jail.networks.length} custom network attachment(s).`}
		confirmLabel="Remove Networks and Save"
		loadingLabel="Saving..."
		loading={saving}
		keepOpenOnConfirm={true}
		actions={{
			onConfirm: save,
			onCancel: () => (removalConfirmationOpen = false)
		}}
	/>
{/if}
