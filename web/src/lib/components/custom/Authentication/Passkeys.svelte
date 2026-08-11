<script lang="ts">
	import {
		beginPasskeyRegistration,
		deleteUserPasskey,
		finishPasskeyRegistration,
		listUserPasskeys
	} from '$lib/api/auth/passkeys';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Passkey } from '$lib/types/auth';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import {
		buildRegistrationOptions,
		isPasskeySupported,
		serializeCredential
	} from '$lib/utils/passkeys';
	import { convertDbTime } from '$lib/utils/time';
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		userId: number;
		username: string;
		hostname: string;
		registrationEligible: boolean;
		reload?: boolean;
	}

	let {
		open = $bindable(),
		userId,
		username,
		hostname,
		registrationEligible,
		reload = $bindable()
	}: Props = $props();
	let loading = $state(false);
	let loadFailed = $state(false);
	let registering = $state(false);
	let deletingCredentialId = $state('');
	let label = $state('');
	let passkeys = $state.raw<Passkey[]>([]);
	let pendingDelete = $state<{ open: boolean; credentialId: string; label: string }>({
		open: false,
		credentialId: '',
		label: ''
	});
	let listController: AbortController | null = null;
	let registrationController: AbortController | null = null;
	let deleteController: AbortController | null = null;

	async function refreshPasskeys() {
		listController?.abort();
		const controller = new AbortController();
		listController = controller;
		loading = true;
		loadFailed = false;
		try {
			const response = await listUserPasskeys(userId, {
				hostname,
				signal: controller.signal
			});
			if (controller.signal.aborted) return;
			if (isAPIResponse(response)) {
				handleAPIError(response);
				passkeys = [];
				loadFailed = true;
				toast.error('Failed to load passkeys', { position: 'bottom-center' });
				return;
			}
			passkeys = response;
		} catch (error) {
			if (isRequestCancellation(error)) return;
			passkeys = [];
			loadFailed = true;
			toast.error('Failed to load passkeys', { position: 'bottom-center' });
		} finally {
			if (listController === controller) {
				listController = null;
				loading = false;
			}
		}
	}

	function closeDialog() {
		listController?.abort();
		registrationController?.abort();
		deleteController?.abort();
		pendingDelete = { open: false, credentialId: '', label: '' };
		open = false;
	}

	onDestroy(() => {
		listController?.abort();
		registrationController?.abort();
		deleteController?.abort();
	});

	watch([() => open, () => userId, () => hostname, () => registrationEligible], ([isOpen]) => {
		listController?.abort();
		registrationController?.abort();
		deleteController?.abort();
		passkeys = [];
		loadFailed = false;
		label = '';
		pendingDelete = { open: false, credentialId: '', label: '' };
		if (isOpen) void refreshPasskeys();
	});

	async function registerPasskey() {
		if (registering || deletingCredentialId || !registrationEligible) return;

		const trimmedLabel = label.trim();
		if (trimmedLabel === '') return;
		if (Array.from(trimmedLabel).length > 128) {
			toast.error('Passkey labels must be 128 characters or fewer', {
				position: 'bottom-center'
			});
			return;
		}

		if (!isPasskeySupported()) {
			toast.error('Passkeys require HTTPS and browser WebAuthn support', {
				position: 'bottom-center'
			});
			return;
		}

		registrationController?.abort();
		const controller = new AbortController();
		registrationController = controller;
		registering = true;
		try {
			const begin = await beginPasskeyRegistration(userId, {
				hostname,
				signal: controller.signal
			});
			if (controller.signal.aborted) return;
			if (isAPIResponse(begin)) {
				handleAPIError(begin);
				toast.error('Could not start passkey registration', {
					position: 'bottom-center'
				});
				return;
			}

			const credential = await navigator.credentials.create({
				publicKey: buildRegistrationOptions(begin.publicKey),
				signal: controller.signal
			});

			if (controller.signal.aborted) return;
			if (!credential || !(credential instanceof PublicKeyCredential)) {
				toast.error('Passkey registration failed', {
					position: 'bottom-center'
				});
				return;
			}

			const finish = await finishPasskeyRegistration(
				begin.requestId,
				serializeCredential(credential),
				trimmedLabel,
				{ hostname, signal: controller.signal }
			);
			if (controller.signal.aborted) return;
			if (isAPIResponse(finish)) {
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
			if (
				isRequestCancellation(error) ||
				(error instanceof DOMException && error.name === 'AbortError')
			) {
				return;
			} else if (error instanceof DOMException && error.name === 'NotAllowedError') {
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
			if (registrationController === controller) {
				registrationController = null;
				registering = false;
			}
		}
	}

	async function removePasskey(credentialId: string): Promise<boolean> {
		if (deletingCredentialId || registering) return false;
		deleteController?.abort();
		const controller = new AbortController();
		deleteController = controller;
		deletingCredentialId = credentialId;
		try {
			const response = await deleteUserPasskey(userId, credentialId, {
				hostname,
				signal: controller.signal
			});
			if (controller.signal.aborted) return false;
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to delete passkey', {
					position: 'bottom-center'
				});
				return false;
			}

			reload = true;
			await refreshPasskeys();
			toast.success('Passkey deleted', {
				position: 'bottom-center'
			});
			return true;
		} catch (error) {
			if (isRequestCancellation(error)) return false;
			toast.error('Failed to delete passkey', { position: 'bottom-center' });
			return false;
		} finally {
			if (deleteController === controller) {
				deleteController = null;
				deletingCredentialId = '';
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 md:min-w-1/4 sm:min-w-1/2 gap-4 p-5 overflow-hidden"
		showCloseButton={true}
		onClose={closeDialog}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--fingerprint]"
					size="h-5 w-5"
					gap="gap-2"
					title={`Passkeys - ${username}`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-2">
			<div class="flex items-center gap-2 min-w-0">
				<CustomValueInput
					placeholder="Hayzam's Laptop"
					bind:value={label}
					classes="w-full min-w-0"
					autocomplete="off"
					disabled={!registrationEligible || registering || Boolean(deletingCredentialId)}
				/>
				<Button
					onclick={registerPasskey}
					disabled={!registrationEligible ||
						registering ||
						Boolean(deletingCredentialId) ||
						label.trim() === ''}
					class="shrink-0"
				>
					{#if registering}
						<span class="icon-[line-md--loading-loop] h-4 w-4"></span>
					{:else}
						Register
					{/if}
				</Button>
			</div>
			{#if !registrationEligible}
				<p class="text-xs text-muted-foreground">
					Registration is available only for administrators with Sylve credentials. Existing
					passkeys can still be removed.
				</p>
			{/if}
		</div>

		<div class="rounded-md border overflow-x-auto">
			{#if loading}
				<div class="p-3 text-sm text-muted-foreground">Loading passkeys...</div>
			{:else if loadFailed}
				<div class="flex items-center justify-between gap-3 p-3 text-sm text-muted-foreground">
					<span>Unable to load passkeys.</span>
					<Button size="sm" variant="outline" onclick={refreshPasskeys}>Retry</Button>
				</div>
			{:else if passkeys.length === 0}
				<div class="p-3 text-sm text-muted-foreground">No passkeys registered.</div>
			{:else}
				<table class="w-full text-sm min-w-[500px]">
					<thead class="border-b">
						<tr>
							<th class="px-3 py-2 text-left">Label</th>
							<th class="px-3 py-2 text-left">Credential ID</th>
							<th class="px-3 py-2 text-left whitespace-nowrap">Created</th>
							<th class="px-3 py-2 text-right">Action</th>
						</tr>
					</thead>
					<tbody>
						{#each passkeys as passkey (passkey.credentialId)}
							<tr class="border-b last:border-b-0">
								<td class="px-3 py-2 max-w-[120px] truncate">{passkey.label || '-'}</td>
								<td
									class="px-3 py-2 font-mono text-xs max-w-[200px] truncate"
									title={passkey.credentialId}>{passkey.credentialId}</td
								>
								<td class="px-3 py-2 whitespace-nowrap">{convertDbTime(passkey.createdAt)}</td>
								<td class="px-3 py-2 text-right whitespace-nowrap">
									<Button
										size="sm"
										variant="outline"
										disabled={registering || Boolean(deletingCredentialId)}
										onclick={() => {
											pendingDelete = {
												open: true,
												credentialId: passkey.credentialId,
												label: passkey.label
											};
										}}
									>
										<SpanWithIcon
											icon="icon-[mdi--delete]"
											size="h-4 w-4"
											gap="gap-2"
											title="Delete"
										/>
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

<AlertDialog
	open={pendingDelete.open}
	names={{
		parent: 'Passkey',
		element: pendingDelete.label || pendingDelete.credentialId
	}}
	actions={{
		onConfirm: async () => {
			if (await removePasskey(pendingDelete.credentialId)) {
				pendingDelete = { open: false, credentialId: '', label: '' };
			}
		},
		onCancel: () => {
			pendingDelete = { open: false, credentialId: '', label: '' };
		}
	}}
	keepOpenOnConfirm={true}
></AlertDialog>
