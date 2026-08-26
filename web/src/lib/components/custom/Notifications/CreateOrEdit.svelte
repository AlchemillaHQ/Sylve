<script lang="ts">
	import { createNotificationTransport, updateNotificationTransport } from '$lib/api/notifications';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { User } from '$lib/types/auth';
	import type { NotificationConfig, NotificationTransportInput } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { SvelteSet } from 'svelte/reactivity';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';
	import { generatePassword } from '$lib/utils/string';

	type TransportType = 'ntfy' | 'pushover' | 'smtp' | 'discord';
	type TransportForm = {
		id?: number;
		name: string;
		type: TransportType;
		enabled: boolean;
		ntfyBaseUrl: string;
		ntfyTopic: string;
		ntfyToken: string;
		ntfyHasAuthToken: boolean;
		pushoverApiToken: string;
		pushoverHasApiToken: boolean;
		pushoverUserKey: string;
		pushoverHasUserKey: boolean;
		smtpHost: string;
		smtpPort: number;
		smtpUsername: string;
		smtpFrom: string;
		smtpUseTls: boolean;
		smtpRecipients: string[];
		smtpPassword: string;
		smtpHasPassword: boolean;
		discordWebhookUrl: string;
	};

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		users: User[];
		transports: NotificationConfig['transports'];
		afterChange: () => void;
	}

	let { open = $bindable(), edit, id, users, transports, afterChange }: Props = $props();

	let loading = $state(false);
	let smtpRecipientsOpen = $state(false);
	const pushoverCredentialPattern = /^[A-Za-z0-9]{30}$/;

	function defaultForm(type: TransportType = 'smtp'): TransportForm {
		return {
			name: '',
			type,
			enabled: false,
			ntfyBaseUrl: 'https://ntfy.sh',
			ntfyTopic: '',
			ntfyToken: '',
			ntfyHasAuthToken: false,
			pushoverApiToken: '',
			pushoverHasApiToken: false,
			pushoverUserKey: '',
			pushoverHasUserKey: false,
			smtpHost: '',
			smtpPort: 587,
			smtpUsername: '',
			smtpFrom: '',
			smtpUseTls: true,
			smtpRecipients: [],
			smtpPassword: '',
			smtpHasPassword: false,
			discordWebhookUrl: ''
		};
	}

	const editingTransport = $derived.by(() => {
		if (edit && id) {
			return transports.find((t) => t.id === id) ?? null;
		}
		return null;
	});

	let form = $state<TransportForm>(defaultForm());

	watch(
		() => open,
		(open) => {
			if (open) {
				if (editingTransport) {
					form = {
						id: editingTransport.id,
						name: editingTransport.name,
						type: editingTransport.type,
						enabled: editingTransport.enabled,
						ntfyBaseUrl: editingTransport.ntfy?.baseUrl ?? 'https://ntfy.sh',
						ntfyTopic: editingTransport.ntfy?.topic ?? '',
						ntfyToken: '',
						ntfyHasAuthToken: editingTransport.ntfy?.hasAuthToken ?? false,
						pushoverApiToken: '',
						pushoverHasApiToken: editingTransport.pushover?.hasApiToken ?? false,
						pushoverUserKey: '',
						pushoverHasUserKey: editingTransport.pushover?.hasUserKey ?? false,
						smtpHost: editingTransport.email?.smtpHost ?? '',
						smtpPort: editingTransport.email?.smtpPort ?? 587,
						smtpUsername: editingTransport.email?.smtpUsername ?? '',
						smtpFrom: editingTransport.email?.smtpFrom ?? '',
						smtpUseTls: editingTransport.email?.smtpUseTls ?? true,
						smtpRecipients: [...(editingTransport.email?.recipients ?? [])],
						smtpPassword: '',
						smtpHasPassword: editingTransport.email?.hasPassword ?? false,
						discordWebhookUrl: editingTransport.discord?.webhookUrl ?? ''
					};
				} else {
					form = defaultForm();
				}
			}
		}
	);

	const smtpRecipientOptions = $derived.by(() => {
		const seen = new SvelteSet<string>();
		const options: { label: string; value: string }[] = [];

		for (const user of users) {
			const email = user.email.trim();
			if (!email || seen.has(email)) {
				continue;
			}

			seen.add(email);
			options.push({
				label: user.username ? `${user.username} <${email}>` : email,
				value: email
			});
		}

		return options;
	});

	function normalizeRecipients(values: string[]): string[] {
		const seen = new SvelteSet<string>();
		const normalized: string[] = [];

		for (const value of values) {
			const recipient = value.trim();
			if (!recipient || seen.has(recipient)) {
				continue;
			}

			seen.add(recipient);
			normalized.push(recipient);
		}

		return normalized;
	}

	function buildEntry(f: TransportForm): NotificationTransportInput {
		return {
			name: f.name.trim(),
			type: f.type,
			enabled: f.enabled,
			ntfy:
				f.type === 'ntfy'
					? {
							baseUrl: f.ntfyBaseUrl,
							topic: f.ntfyTopic,
							...(f.ntfyToken.trim().length > 0 ? { authToken: f.ntfyToken.trim() } : {})
						}
					: null,
			pushover:
				f.type === 'pushover'
					? {
							...(f.pushoverApiToken.trim().length > 0
								? { apiToken: f.pushoverApiToken.trim() }
								: {}),
							...(f.pushoverUserKey.trim().length > 0 ? { userKey: f.pushoverUserKey.trim() } : {})
						}
					: null,
			email:
				f.type === 'smtp'
					? {
							smtpHost: f.smtpHost,
							smtpPort: Number(f.smtpPort) || 587,
							smtpUsername: f.smtpUsername,
							smtpFrom: f.smtpFrom,
							smtpUseTls: f.smtpUseTls,
							recipients: normalizeRecipients(f.smtpRecipients),
							...(f.smtpPassword.trim().length > 0 ? { smtpPassword: f.smtpPassword.trim() } : {})
						}
					: null,
			discord:
				f.type === 'discord'
					? {
							...(f.discordWebhookUrl.trim().length > 0
								? { webhookUrl: f.discordWebhookUrl.trim() }
								: {})
						}
					: null
		};
	}

	function resetForm() {
		if (editingTransport) {
			form = {
				id: editingTransport.id,
				name: editingTransport.name,
				type: editingTransport.type,
				enabled: editingTransport.enabled,
				ntfyBaseUrl: editingTransport.ntfy?.baseUrl ?? 'https://ntfy.sh',
				ntfyTopic: editingTransport.ntfy?.topic ?? '',
				ntfyToken: '',
				ntfyHasAuthToken: editingTransport.ntfy?.hasAuthToken ?? false,
				pushoverApiToken: '',
				pushoverHasApiToken: editingTransport.pushover?.hasApiToken ?? false,
				pushoverUserKey: '',
				pushoverHasUserKey: editingTransport.pushover?.hasUserKey ?? false,
				smtpHost: editingTransport.email?.smtpHost ?? '',
				smtpPort: editingTransport.email?.smtpPort ?? 587,
				smtpUsername: editingTransport.email?.smtpUsername ?? '',
				smtpFrom: editingTransport.email?.smtpFrom ?? '',
				smtpUseTls: editingTransport.email?.smtpUseTls ?? true,
				smtpRecipients: [...(editingTransport.email?.recipients ?? [])],
				smtpPassword: '',
				smtpHasPassword: editingTransport.email?.hasPassword ?? false,
				discordWebhookUrl: editingTransport.discord?.webhookUrl ?? ''
			};
		}
	}

	async function save() {
		if (edit && !form.id) {
			toast.error('Transport is no longer available', {
				duration: 5000,
				position: 'bottom-center'
			});
			return;
		}

		if (form.name.trim().length === 0) {
			toast.error('Transport name is required', {
				duration: 5000,
				position: 'bottom-center'
			});
			return;
		}

		if (form.type === 'smtp') {
			if (form.smtpFrom.trim().length === 0) {
				toast.error('From Email is required', { duration: 5000, position: 'bottom-center' });
				return;
			}
			if (form.smtpHost.trim().length === 0) {
				toast.error('SMTP Host is required', { duration: 5000, position: 'bottom-center' });
				return;
			}
			if (form.smtpRecipients.length === 0) {
				toast.error('At least one recipient is required', {
					duration: 5000,
					position: 'bottom-center'
				});
				return;
			}
		} else if (form.type === 'ntfy') {
			if (form.ntfyTopic.trim().length === 0) {
				toast.error('Topic is required', { duration: 5000, position: 'bottom-center' });
				return;
			}
		} else if (form.type === 'pushover') {
			if (form.pushoverApiToken.trim().length === 0 && !form.pushoverHasApiToken) {
				toast.error('Application API token is required', {
					duration: 5000,
					position: 'bottom-center'
				});
				return;
			}
			if (
				form.pushoverApiToken.trim().length > 0 &&
				!pushoverCredentialPattern.test(form.pushoverApiToken.trim())
			) {
				toast.error('Application API token must be 30 letters or numbers', {
					duration: 5000,
					position: 'bottom-center'
				});
				return;
			}
			if (form.pushoverUserKey.trim().length === 0 && !form.pushoverHasUserKey) {
				toast.error('User or group key is required', {
					duration: 5000,
					position: 'bottom-center'
				});
				return;
			}
			if (
				form.pushoverUserKey.trim().length > 0 &&
				!pushoverCredentialPattern.test(form.pushoverUserKey.trim())
			) {
				toast.error('User or group key must be 30 letters or numbers', {
					duration: 5000,
					position: 'bottom-center'
				});
				return;
			}
		} else if (form.type === 'discord') {
			if (form.discordWebhookUrl.trim().length === 0) {
				toast.error('Webhook URL is required', { duration: 5000, position: 'bottom-center' });
				return;
			}
		}

		loading = true;

		const entry = buildEntry(form);
		const response = edit
			? await updateNotificationTransport(form.id as number, entry)
			: await createNotificationTransport(entry);
		loading = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error(`Failed to ${edit ? 'update' : 'create'} transport`, {
				duration: 5000,
				position: 'bottom-center'
			});
			return;
		}

		toast.success(`Transport ${edit ? 'updated' : 'created'}`, {
			duration: 3500,
			position: 'bottom-center'
		});
		open = false;
		afterChange();
	}
</script>

<input type="text" style="display:none;" name="dummy_username" />
<input type="password" style="display:none;" name="dummy_password" />

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] overflow-y-auto sm:max-w-140"
		showCloseButton={true}
		showResetButton={edit && !!editingTransport}
		onClose={() => (open = false)}
		onReset={resetForm}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mingcute--mail-ai-line]"
					size="h-5 w-5"
					gap="gap-2"
					title={edit ? 'Edit Transport' : 'New Transport'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-4">
			<div class="grid gap-3 sm:grid-cols-2">
				<CustomValueInput
					label="Transport Name"
					bind:value={form.name}
					placeholder="Common Transport"
				/>
				<SimpleSelect
					label="Type"
					options={[
						{ value: 'ntfy', label: 'ntfy' },
						{ value: 'pushover', label: 'Pushover' },
						{ value: 'smtp', label: 'SMTP' },
						{ value: 'discord', label: 'Discord' }
					]}
					bind:value={form.type}
					onChange={(v) => (form.type = v as TransportType)}
					classes={{
						parent: 'flex-1 min-w-0 space-y-1.5',
						label: 'text-sm font-medium whitespace-nowrap h-7',
						trigger:
							'inline-flex h-8 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>
			</div>

			{#if form.type === 'ntfy'}
				<div class="space-y-3">
					<CustomValueInput
						label="Base URL"
						bind:value={form.ntfyBaseUrl}
						placeholder="https://ntfy.sh"
					/>
					<CustomValueInput
						label="Topic"
						bind:value={form.ntfyTopic}
						placeholder="sylve-events-5678"
						topRightButton={{
							icon: 'icon-[oui--generate]',
							tooltip: 'Generate random topic0',
							function: async () => {
								return generatePassword();
							}
						}}
					/>
					<CustomValueInput
						label="Auth Token"
						type="password"
						bind:value={form.ntfyToken}
						placeholder={form.ntfyHasAuthToken ? 'Token stored (leave blank to keep)' : 'Optional'}
						revealOnFocus={true}
					/>
					<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
				</div>
			{:else if form.type === 'pushover'}
				<div class="space-y-3">
					<CustomValueInput
						label="Application API Token"
						type="password"
						bind:value={form.pushoverApiToken}
						placeholder={form.pushoverHasApiToken
							? 'Token stored (leave blank to keep)'
							: 'Required'}
						revealOnFocus={true}
					/>
					<CustomValueInput
						label="User / Group Key"
						type="password"
						bind:value={form.pushoverUserKey}
						placeholder={form.pushoverHasUserKey ? 'Key stored (leave blank to keep)' : 'Required'}
						revealOnFocus={true}
					/>
					<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
				</div>
			{:else if form.type === 'discord'}
				<div class="space-y-3">
					<CustomValueInput
						label="Webhook URL"
						bind:value={form.discordWebhookUrl}
						placeholder="https://discord.com/api/webhooks/..."
					/>
					<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
				</div>
			{:else}
				<div class="space-y-3">
					<div class="grid gap-3 sm:grid-cols-2">
						<CustomValueInput
							label="SMTP Host"
							bind:value={form.smtpHost}
							placeholder="smtp.gmail.com"
						/>
						<CustomValueInput
							label="SMTP Port"
							type="number"
							bind:value={form.smtpPort}
							placeholder="587"
						/>
						<CustomValueInput
							label="SMTP Username"
							bind:value={form.smtpUsername}
							placeholder="user@example.com"
						/>
						<CustomValueInput
							label="From Email"
							bind:value={form.smtpFrom}
							placeholder="user@example.com"
						/>
					</div>
					<CustomValueInput
						label="SMTP Password"
						type="password"
						bind:value={form.smtpPassword}
						placeholder={form.smtpHasPassword
							? 'Password stored (leave blank to keep)'
							: 'Optional'}
						revealOnFocus={true}
					/>
					<ComboBox
						bind:open={smtpRecipientsOpen}
						label="Recipients"
						bind:value={form.smtpRecipients}
						data={smtpRecipientOptions}
						placeholder="Select or type recipients"
						width="w-full"
						multiple={true}
						allowCustom={true}
						onValueChange={(value) => {
							form.smtpRecipients = normalizeRecipients(Array.isArray(value) ? value : []);
						}}
					/>
					<div class="grid grid-cols-2 gap-x-4">
						<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
						<CustomCheckbox label="Use TLS/STARTTLS" bind:checked={form.smtpUseTls} />
					</div>
				</div>
			{/if}
		</div>

		<Dialog.Footer>
			<Button onclick={save} disabled={loading}>
				{#if loading}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
				{/if}
				{edit ? 'Save' : 'Create'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
