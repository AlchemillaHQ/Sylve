<script lang="ts">
	import { changeADMembership, getActiveDirectory } from '$lib/api/directory-services';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ActiveDirectory } from '$lib/types/directory-services';
	import { getAPIErrorText } from '$lib/utils/http';
	import { formatDirectoryServiceError } from '$lib/utils/directory-services';
	import { reportAPIError } from '$lib/stores/error-details.svelte';
	import { toast } from 'svelte-sonner';
	import { onDestroy, untrack } from 'svelte';

	let {
		open = $bindable(),
		ad,
		node,
		leave = false,
		reload = $bindable(false)
	}: {
		open: boolean;
		node: string;
		ad: ActiveDirectory;
		leave?: boolean;
		reload?: boolean;
	} = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
		password = '';
		username = '';
	});
	let username = $state(
		untrack(() => (!leave && !ad.keytabId && !ad.joinPasswordConfigured ? ad.joinUsername : ''))
	);
	let password = $state('');
	let busy = $state(false);
	let localOnly = $state(false);
	function close() {
		if (busy) return;
		username = '';
		password = '';
		open = false;
	}
	async function submit() {
		if (busy) return;
		if (
			!leave &&
			!(ad.joined && ad.membershipPending === 'join') &&
			!ad.keytabId &&
			!ad.joinPasswordConfigured &&
			(!username.trim() || !password)
		) {
			toast.error('Join credentials are required.', { position: 'bottom-center' });
			return;
		}
		busy = true;
		const useSavedCredentials = !leave && !ad.keytabId && ad.joinPasswordConfigured;
		try {
			const result = await changeADMembership(
				leave,
				localOnly || useSavedCredentials ? '' : username.trim(),
				localOnly || useSavedCredentials ? '' : password,
				localOnly,
				{ hostname: targetNode }
			);
			password = '';
			if (!mounted) return;
			reload = true;
			if (result.status === 'error') {
				reportAPIError(result, formatDirectoryServiceError(getAPIErrorText(result)));
				return;
			}
			let warning = '';
			if (!leave) {
				try {
					warning = (await getActiveDirectory({ hostname: targetNode })).joinWarning;
				} catch {
					// The page reload still retrieves any saved post-join warning.
				}
				if (!mounted) return;
			}
			if (warning) toast.warning(warning, { position: 'bottom-center' });
			else
				toast.success(leave ? 'Domain membership removed.' : 'Domain joined.', {
					position: 'bottom-center'
				});
			username = '';
			open = false;
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
		showCloseButton={!busy}
		onClose={close}
		onEscapeKeydown={(event) => {
			if (busy) event.preventDefault();
		}}
	>
		<Dialog.Header
			><Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--account-key-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title={leave ? 'Leave Active Directory Domain' : 'Join Active Directory Domain'}
				/></Dialog.Title
			><Dialog.Description class="text-muted-foreground text-xs"
				>{ad.domain} · {ad.computerName}</Dialog.Description
			></Dialog.Header
		>
		<div class="space-y-2">
			{#if leave}
				<Card.Root class="border-yellow-500/30 bg-yellow-500/10 p-0!" role="alert">
					<Card.Content
						class="flex items-start gap-2 p-3 text-sm text-yellow-600 dark:text-yellow-400"
					>
						<span
							class="icon-[material-symbols--warning] mt-0.5 h-4 w-4 shrink-0"
							aria-hidden="true"
						></span>
						<div class="min-w-0 space-y-2">
							<p>
								Leaving removes this node's domain membership. Existing domain share permissions and
								filesystem ACLs remain recorded, but domain users will lose access.
							</p>
							{#if localOnly && ad.membershipPending !== 'leave-cleanup'}
								<p>
									Remove local membership <b>ONLY</b> when the domain is unavailable. This disables local
									integration and removes Sylve's machine keytab; it does not delete the AD computer account
									or Samba's cached trust secrets. Remove the computer account in AD separately.
								</p>
							{/if}
						</div>
					</Card.Content>
				</Card.Root>
			{/if}
			{#if leave && ad.membershipPending === 'leave-cleanup'}<p
					class="text-muted-foreground text-xs"
				>
					Domain integration is disabled. Retry finishes local cleanup without contacting the
					domain.
				</p>{:else if !leave && ad.joined && ad.membershipPending === 'join'}<p
					class="text-muted-foreground text-xs"
				>
					The domain join is confirmed. Retry completes local configuration without enrolling again.
				</p>{:else}
				{#if !leave}
					<p class="break-words text-sm">
						Joining registers {ad.computerName} with {ad.domain} and enables Active Directory integration
						on this node. Samba access remains controlled by each share's permissions.
						{#if ad.skipDNSRegistration}AD DNS registration will be skipped; manage this member's
							DNS record separately.{/if}
						{#if ad.keytabId}
							Using keytab principal {ad.kerberosPrincipal}. No password entry is needed here.
						{:else if ad.joinPasswordConfigured}
							Using saved credentials for {ad.joinUsername}. No password entry is needed here.
						{:else}
							Use a domain account with permission to join this computer. These credentials are used
							for this join only and are not saved.
						{/if}
					</p>
				{/if}
				{#if (!ad.keytabId || leave) && !localOnly && (leave || !ad.joinPasswordConfigured)}
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<CustomValueInput
							label="Username"
							placeholder={leave ? 'Optional; machine account by default' : 'joinuser@EXAMPLE.COM'}
							bind:value={username}
							disabled={busy}
							autocomplete="off"
						/><CustomValueInput
							label="Password"
							placeholder={leave ? 'Optional' : 'Join password'}
							type="password"
							bind:value={password}
							disabled={busy}
							autocomplete="new-password"
						/>
					</div>
				{/if}
			{/if}
		</div>
		{#if leave && ad.membershipPending !== 'leave-cleanup'}
			<CustomCheckbox
				label="Remove Local Membership Only"
				bind:checked={localOnly}
				disabled={busy}
			/>
		{/if}
		<Dialog.Footer class="flex justify-end"
			><Button variant={leave ? 'destructive' : 'default'} disabled={busy} onclick={submit}
				>{#if busy}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>{leave
						? 'Leaving...'
						: 'Joining...'}{:else}{leave ? 'Leave Domain' : 'Join Domain'}{/if}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
