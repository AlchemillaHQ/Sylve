<script lang="ts">
	import {
		PasskeyChallengeSchema,
		beginPasskeyRegistration,
		deleteUserPasskey,
		finishPasskeyRegistration,
		listUserPasskeys
	} from '$lib/api/auth/passkeys';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Passkey } from '$lib/types/auth';
	import { handleAPIError } from '$lib/utils/http';
	import {
		buildRegistrationOptions,
		isPasskeySupported,
		serializeCredential
	} from '$lib/utils/passkeys';
	import { convertDbTime } from '$lib/utils/time';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		userId: number;
		username: string;
		reload?: boolean;
	}

	let { open = $bindable(), userId, username, reload = $bindable() }: Props = $props();
	let loading = $state(false);
	let registering = $state(false);
	let label = $state('');
	let passkeys = $state<Passkey[]>([]);

	async function refreshPasskeys() {
		loading = true;
		try {
			passkeys = await listUserPasskeys(userId);
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if (open) {
			void refreshPasskeys();
		}
	});

	async function registerPasskey() {
		if (!isPasskeySupported()) {
			toast.error('Passkeys require HTTPS and browser WebAuthn support', {
				position: 'bottom-center'
			});
			return;
		}

		registering = true;
		try {
			const begin = await beginPasskeyRegistration(userId);
			if (begin.status !== 'success') {
				handleAPIError(begin);
				toast.error('Could not start passkey registration', {
					position: 'bottom-center'
				});
				return;
			}

			const parsed = PasskeyChallengeSchema.safeParse(begin.data);
			if (!parsed.success) {
				toast.error('Invalid registration challenge payload', {
					position: 'bottom-center'
				});
				return;
			}

			const credential = await navigator.credentials.create({
				publicKey: buildRegistrationOptions(parsed.data.publicKey)
			});

			if (!credential || !(credential instanceof PublicKeyCredential)) {
				toast.error('Passkey registration failed', {
					position: 'bottom-center'
				});
				return;
			}

			const finish = await finishPasskeyRegistration(
				parsed.data.requestId,
				serializeCredential(credential),
				label
			);
			if (finish.status !== 'success') {
				handleAPIError(finish);
				toast.error('Could not finish passkey registration', {
					position: 'bottom-center'
				});
				return;
			}

			label = '';
			reload = true;
			await refreshPasskeys();
			toast.success('Passkey registered', {
				position: 'bottom-center'
			});
		} catch (error) {
			if (error instanceof DOMException && error.name === 'NotAllowedError') {
				toast.error('Passkey request cancelled or timed out', {
					position: 'bottom-center'
				});
			} else {
				console.error('Passkey registration error:', error);
				toast.error('Failed to register passkey', {
					position: 'bottom-center'
				});
			}
		} finally {
			registering = false;
		}
	}

	async function removePasskey(credentialId: string) {
		const response = await deleteUserPasskey(userId, credentialId);
		if (response.status !== 'success') {
			handleAPIError(response);
			toast.error('Failed to delete passkey', {
				position: 'bottom-center'
			});
			return;
		}

		reload = true;
		await refreshPasskeys();
		toast.success('Passkey deleted', {
			position: 'bottom-center'
		});
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-full min-w-2xl gap-4 p-5">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--fingerprint] h-5 w-5"></span>
					<span>Passkeys - {username}</span>
				</div>
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
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-2">
			<div class="flex items-center gap-2">
				<CustomValueInput
					placeholder="Hayzam's Laptop"
					bind:value={label}
					classes="w-full"
					autocomplete="off"
				/>
				<Button onclick={registerPasskey} disabled={registering || label.trim() === ''}>
					{#if registering}
						<span class="icon-[line-md--loading-loop] h-4 w-4"></span>
					{:else}
						Register
					{/if}
				</Button>
			</div>
		</div>

		<div class="rounded-md border">
			{#if loading}
				<div class="p-3 text-sm text-muted-foreground">Loading passkeys...</div>
			{:else if passkeys.length === 0}
				<div class="p-3 text-sm text-muted-foreground">No passkeys registered.</div>
			{:else}
				<table class="w-full text-sm">
					<thead class="border-b">
						<tr>
							<th class="px-3 py-2 text-left">Label</th>
							<th class="px-3 py-2 text-left">Credential ID</th>
							<th class="px-3 py-2 text-left">Created</th>
							<th class="px-3 py-2 text-right">Action</th>
						</tr>
					</thead>
					<tbody>
						{#each passkeys as passkey}
							<tr class="border-b last:border-b-0">
								<td class="px-3 py-2">{passkey.label || '-'}</td>
								<td class="px-3 py-2 font-mono text-xs">{passkey.credentialId}</td>
								<td class="px-3 py-2">{convertDbTime(passkey.createdAt)}</td>
								<td class="px-3 py-2 text-right">
									<Button
										size="sm"
										variant="outline"
										onclick={() => {
											void removePasskey(passkey.credentialId);
										}}
									>
										<span class="icon-[material-symbols--delete-outline] h-4 w-4"></span>
										<span class="sr-only">Delete</span>
									</Button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
