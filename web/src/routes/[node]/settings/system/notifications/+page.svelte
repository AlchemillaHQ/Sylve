<script lang="ts">
	import { getNotificationConfig, updateNotificationConfig } from '$lib/api/notifications';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { NotificationConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		config: NotificationConfig;
	}

	let { data }: { data: Data } = $props();
	const fallbackConfig = data.config;

	const configResource = resource(
		() => 'notification-config',
		async () => {
			const loaded = await getNotificationConfig();
			if (isAPIResponse(loaded)) {
				return fallbackConfig;
			}
			updateCache('notification-config', loaded);
			return loaded;
		},
		{ initialValue: data.config }
	);

	let loading = $state(false);
	let ntfyEnabled = $state(configResource.current.ntfy.enabled);
	let ntfyBaseUrl = $state(configResource.current.ntfy.baseUrl);
	let ntfyTopic = $state(configResource.current.ntfy.topic);
	let ntfyToken = $state('');
	let clearNtfyToken = $state(false);

	let emailEnabled = $state(configResource.current.email.enabled);
	let smtpHost = $state(configResource.current.email.smtpHost);
	let smtpPort = $state(configResource.current.email.smtpPort || 587);
	let smtpUsername = $state(configResource.current.email.smtpUsername);
	let smtpFrom = $state(configResource.current.email.smtpFrom);
	let smtpUseTls = $state(configResource.current.email.smtpUseTls);
	let smtpRecipients = $state((configResource.current.email.recipients || []).join(', '));
	let smtpPassword = $state('');
	let clearSMTPPassword = $state(false);

	function hydrateFromConfig(next: NotificationConfig) {
		ntfyEnabled = next.ntfy.enabled;
		ntfyBaseUrl = next.ntfy.baseUrl;
		ntfyTopic = next.ntfy.topic;
		emailEnabled = next.email.enabled;
		smtpHost = next.email.smtpHost;
		smtpPort = next.email.smtpPort || 587;
		smtpUsername = next.email.smtpUsername;
		smtpFrom = next.email.smtpFrom;
		smtpUseTls = next.email.smtpUseTls;
		smtpRecipients = (next.email.recipients || []).join(', ');
		smtpPassword = '';
		ntfyToken = '';
		clearNtfyToken = false;
		clearSMTPPassword = false;
	}

	async function refreshConfig() {
		await configResource.refetch();
		hydrateFromConfig(configResource.current);
	}

	function parseRecipients(input: string): string[] {
		return input
			.split(/[\n,]+/g)
			.map((item) => item.trim())
			.filter((item) => item.length > 0);
	}

	async function saveConfig() {
		loading = true;

		const payload = {
			ntfy: {
				enabled: ntfyEnabled,
				baseUrl: ntfyBaseUrl,
				topic: ntfyTopic,
				...(ntfyToken.trim().length > 0 || clearNtfyToken
					? { authToken: clearNtfyToken ? '' : ntfyToken.trim() }
					: {})
			},
			email: {
				enabled: emailEnabled,
				smtpHost,
				smtpPort: Number(smtpPort) || 587,
				smtpUsername,
				smtpFrom,
				smtpUseTls,
				recipients: parseRecipients(smtpRecipients),
				...(smtpPassword.trim().length > 0 || clearSMTPPassword
					? { smtpPassword: clearSMTPPassword ? '' : smtpPassword.trim() }
					: {})
			}
		};

		const response = await updateNotificationConfig(payload);
		loading = false;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update notification config', {
				duration: 5000,
				position: 'bottom-center'
			});
			return;
		}

		updateCache('notification-config', response);
		hydrateFromConfig(response as NotificationConfig);
		toast.success('Notification config updated', {
			duration: 3500,
			position: 'bottom-center'
		});
	}
</script>

<div class="p-4 md:p-6">
	<div class="mb-5 flex items-center justify-between gap-3">
		<div>
			<h2 class="text-lg font-semibold">Notifications</h2>
			<p class="text-muted-foreground text-sm">
				Configure notification transports. UI notifications are always enabled.
			</p>
		</div>
		<div class="flex gap-2">
			<Button size="sm" variant="outline" class="h-7" onclick={refreshConfig}>Refresh</Button>
			<Button size="sm" class="h-7" onclick={saveConfig} disabled={loading}>
				{#if loading}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
				{/if}
				Save
			</Button>
		</div>
	</div>

	<div class="space-y-5">
		<section class="rounded-md border p-4">
			<div class="mb-3 flex items-center justify-between">
				<h3 class="font-medium">ntfy.sh</h3>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={ntfyEnabled} />
					Enabled
				</label>
			</div>

			<div class="grid gap-3 md:grid-cols-2">
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">Base URL</span>
					<input class="w-full rounded-md border px-2 py-1.5" bind:value={ntfyBaseUrl} />
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">Topic</span>
					<input class="w-full rounded-md border px-2 py-1.5" bind:value={ntfyTopic} />
				</label>
			</div>

			<div class="mt-3 grid gap-3 md:grid-cols-2">
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">Auth Token</span>
					<input
						type="password"
						class="w-full rounded-md border px-2 py-1.5"
						placeholder={configResource.current.ntfy.hasAuthToken
							? 'Token stored (leave blank to keep)'
							: 'Optional'}
						bind:value={ntfyToken}
					/>
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">Stored Token</span>
					<div class="flex h-[34px] items-center gap-2 rounded-md border px-2 py-1.5">
						<span class="text-xs">
							{configResource.current.ntfy.hasAuthToken ? 'Configured' : 'Not configured'}
						</span>
						<label class="ml-auto flex items-center gap-1 text-xs">
							<input type="checkbox" bind:checked={clearNtfyToken} />
							Clear
						</label>
					</div>
				</label>
			</div>
		</section>

		<section class="rounded-md border p-4">
			<div class="mb-3 flex items-center justify-between">
				<h3 class="font-medium">Email (SMTP)</h3>
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={emailEnabled} />
					Enabled
				</label>
			</div>

			<div class="grid gap-3 md:grid-cols-2">
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">SMTP Host</span>
					<input class="w-full rounded-md border px-2 py-1.5" bind:value={smtpHost} />
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">SMTP Port</span>
					<input type="number" class="w-full rounded-md border px-2 py-1.5" bind:value={smtpPort} />
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">SMTP Username</span>
					<input class="w-full rounded-md border px-2 py-1.5" bind:value={smtpUsername} />
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">From Email</span>
					<input class="w-full rounded-md border px-2 py-1.5" bind:value={smtpFrom} />
				</label>
			</div>

			<div class="mt-3 grid gap-3 md:grid-cols-2">
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">SMTP Password</span>
					<input
						type="password"
						class="w-full rounded-md border px-2 py-1.5"
						placeholder={configResource.current.email.hasPassword
							? 'Password stored (leave blank to keep)'
							: 'Optional'}
						bind:value={smtpPassword}
					/>
				</label>
				<label class="text-sm">
					<span class="mb-1 block text-xs text-muted-foreground">Stored Password</span>
					<div class="flex h-[34px] items-center gap-2 rounded-md border px-2 py-1.5">
						<span class="text-xs">
							{configResource.current.email.hasPassword ? 'Configured' : 'Not configured'}
						</span>
						<label class="ml-auto flex items-center gap-1 text-xs">
							<input type="checkbox" bind:checked={clearSMTPPassword} />
							Clear
						</label>
					</div>
				</label>
			</div>

			<label class="mt-3 block text-sm">
				<span class="mb-1 block text-xs text-muted-foreground">Recipients (comma or newline separated)</span>
				<textarea class="min-h-24 w-full rounded-md border px-2 py-1.5" bind:value={smtpRecipients}
				></textarea>
			</label>

			<label class="mt-3 flex items-center gap-2 text-sm">
				<input type="checkbox" bind:checked={smtpUseTls} />
				Use TLS/STARTTLS
			</label>
		</section>
	</div>
</div>
