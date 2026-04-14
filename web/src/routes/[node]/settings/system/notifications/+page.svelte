<script lang="ts">
	import { getNotificationTransports, updateNotificationTransports } from '$lib/api/notifications';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { NotificationConfig, UpdateNotificationConfigInput } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		config: NotificationConfig;
	}

	type TransportType = 'ntfy' | 'smtp';
	type TransportForm = {
		id?: number;
		name: string;
		type: TransportType;
		enabled: boolean;
		ntfyBaseUrl: string;
		ntfyTopic: string;
		ntfyToken: string;
		ntfyHasAuthToken: boolean;
		clearNtfyToken: boolean;
		smtpHost: string;
		smtpPort: number;
		smtpUsername: string;
		smtpFrom: string;
		smtpUseTls: boolean;
		smtpRecipients: string;
		smtpPassword: string;
		smtpHasPassword: boolean;
		clearSMTPPassword: boolean;
	};

	let { data }: { data: Data } = $props();
	const fallbackConfig = data.config;

	const configResource = resource(
		() => 'notification-config',
		async () => {
			const loaded = await getNotificationTransports();
			if (isAPIResponse(loaded)) {
				return fallbackConfig;
			}
			updateCache('notification-config', loaded);
			return loaded;
		},
		{ initialValue: data.config }
	);

	let loading = $state(false);
	let transports = $state<TransportForm[]>([]);

	function defaultTransport(index = 1, type: TransportType = 'smtp'): TransportForm {
		return {
			name: `Transport ${index}`,
			type,
			enabled: false,
			ntfyBaseUrl: 'https://ntfy.sh',
			ntfyTopic: '',
			ntfyToken: '',
			ntfyHasAuthToken: false,
			clearNtfyToken: false,
			smtpHost: '',
			smtpPort: 587,
			smtpUsername: '',
			smtpFrom: '',
			smtpUseTls: true,
			smtpRecipients: '',
			smtpPassword: '',
			smtpHasPassword: false,
			clearSMTPPassword: false
		};
	}

	function hydrateFromConfig(next: NotificationConfig) {
		const rows = (next.transports || []).map((transport, index) => ({
			id: transport.id,
			name: transport.name || `Transport ${index + 1}`,
			type: transport.type,
			enabled: transport.enabled,
			ntfyBaseUrl: transport.ntfy?.baseUrl || 'https://ntfy.sh',
			ntfyTopic: transport.ntfy?.topic || '',
			ntfyToken: '',
			ntfyHasAuthToken: transport.ntfy?.hasAuthToken || false,
			clearNtfyToken: false,
			smtpHost: transport.email?.smtpHost || '',
			smtpPort: transport.email?.smtpPort || 587,
			smtpUsername: transport.email?.smtpUsername || '',
			smtpFrom: transport.email?.smtpFrom || '',
			smtpUseTls: transport.email?.smtpUseTls ?? true,
			smtpRecipients: (transport.email?.recipients || []).join(', '),
			smtpPassword: '',
			smtpHasPassword: transport.email?.hasPassword || false,
			clearSMTPPassword: false
		}));

		transports = rows;
	}

	function parseRecipients(input: string): string[] {
		return input
			.split(/[\n,]+/g)
			.map((item) => item.trim())
			.filter((item) => item.length > 0);
	}

	function addTransport(type: TransportType) {
		transports = [...transports, defaultTransport(transports.length + 1, type)];
	}

	function removeTransport(index: number) {
		transports = transports.filter((_, i) => i !== index);
	}

	async function refreshConfig() {
		await configResource.refetch();
		hydrateFromConfig(configResource.current);
	}

	async function saveConfig() {
		loading = true;

		const payload: UpdateNotificationConfigInput = {
			transports: transports.map((transport) => ({
				...(transport.id ? { id: transport.id } : {}),
				name: transport.name.trim() || 'Default',
				type: transport.type,
				enabled: transport.enabled,
				ntfy:
					transport.type === 'ntfy'
						? {
								baseUrl: transport.ntfyBaseUrl,
								topic: transport.ntfyTopic,
								...(transport.ntfyToken.trim().length > 0 || transport.clearNtfyToken
									? { authToken: transport.clearNtfyToken ? '' : transport.ntfyToken.trim() }
									: {})
							}
						: null,
				email:
					transport.type === 'smtp'
						? {
								smtpHost: transport.smtpHost,
								smtpPort: Number(transport.smtpPort) || 587,
								smtpUsername: transport.smtpUsername,
								smtpFrom: transport.smtpFrom,
								smtpUseTls: transport.smtpUseTls,
								recipients: parseRecipients(transport.smtpRecipients),
								...(transport.smtpPassword.trim().length > 0 || transport.clearSMTPPassword
									? { smtpPassword: transport.clearSMTPPassword ? '' : transport.smtpPassword.trim() }
									: {})
							}
						: null
			}))
		};

		const response = await updateNotificationTransports(payload);
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

	hydrateFromConfig(configResource.current);
</script>

<div class="p-4 md:p-6">
	<div class="mb-5 flex items-center justify-between gap-3">
		<div>
			<h2 class="text-lg font-semibold">Notifications</h2>
			<p class="text-muted-foreground text-sm">Configure one row per transport. UI notifications are always enabled.</p>
		</div>
		<div class="flex gap-2">
			<Button size="sm" variant="outline" class="h-7" onclick={() => addTransport('ntfy')}>Add ntfy</Button>
			<Button size="sm" variant="outline" class="h-7" onclick={() => addTransport('smtp')}>Add SMTP</Button>
			<Button size="sm" variant="outline" class="h-7" onclick={refreshConfig}>Refresh</Button>
			<Button size="sm" class="h-7" onclick={saveConfig} disabled={loading}>
				{#if loading}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
				{/if}
				Save
			</Button>
		</div>
	</div>

	{#if transports.length === 0}
		<div class="rounded-md border p-4 text-sm text-muted-foreground">No transports configured.</div>
	{:else}
		<div class="space-y-5">
			{#each transports as transport, index}
				<section class="rounded-md border p-4">
					<div class="mb-4 flex items-end justify-between gap-2">
						<div class="grid flex-1 gap-3 md:grid-cols-3">
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Transport Name</span>
								<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.name} />
							</label>
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Type</span>
								<select class="w-full rounded-md border px-2 py-1.5" bind:value={transport.type}>
									<option value="ntfy">ntfy</option>
									<option value="smtp">smtp</option>
								</select>
							</label>
							<label class="flex items-center gap-2 text-sm">
								<input type="checkbox" bind:checked={transport.enabled} />
								Enabled
							</label>
						</div>
						<Button size="sm" variant="destructive" class="h-7" onclick={() => removeTransport(index)}>
							Remove
						</Button>
					</div>

					{#if transport.type === 'ntfy'}
						<div class="space-y-3">
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Base URL</span>
								<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.ntfyBaseUrl} />
							</label>
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Topic</span>
								<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.ntfyTopic} />
							</label>
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Auth Token</span>
								<input
									type="password"
									class="w-full rounded-md border px-2 py-1.5"
									placeholder={transport.ntfyHasAuthToken ? 'Token stored (leave blank to keep)' : 'Optional'}
									bind:value={transport.ntfyToken}
								/>
							</label>
							<label class="flex items-center gap-2 text-xs">
								<input type="checkbox" bind:checked={transport.clearNtfyToken} />
								Clear stored token ({transport.ntfyHasAuthToken ? 'configured' : 'not configured'})
							</label>
						</div>
					{:else}
						<div class="space-y-3">
							<div class="grid gap-3 md:grid-cols-2">
								<label class="text-sm">
									<span class="mb-1 block text-xs text-muted-foreground">SMTP Host</span>
									<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.smtpHost} />
								</label>
								<label class="text-sm">
									<span class="mb-1 block text-xs text-muted-foreground">SMTP Port</span>
									<input type="number" class="w-full rounded-md border px-2 py-1.5" bind:value={transport.smtpPort} />
								</label>
								<label class="text-sm">
									<span class="mb-1 block text-xs text-muted-foreground">SMTP Username</span>
									<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.smtpUsername} />
								</label>
								<label class="text-sm">
									<span class="mb-1 block text-xs text-muted-foreground">From Email</span>
									<input class="w-full rounded-md border px-2 py-1.5" bind:value={transport.smtpFrom} />
								</label>
							</div>
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">SMTP Password</span>
								<input
									type="password"
									class="w-full rounded-md border px-2 py-1.5"
									placeholder={transport.smtpHasPassword ? 'Password stored (leave blank to keep)' : 'Optional'}
									bind:value={transport.smtpPassword}
								/>
							</label>
							<label class="flex items-center gap-2 text-xs">
								<input type="checkbox" bind:checked={transport.clearSMTPPassword} />
								Clear stored password ({transport.smtpHasPassword ? 'configured' : 'not configured'})
							</label>
							<label class="text-sm">
								<span class="mb-1 block text-xs text-muted-foreground">Recipients (comma or newline separated)</span>
								<textarea class="min-h-24 w-full rounded-md border px-2 py-1.5" bind:value={transport.smtpRecipients}
								></textarea>
							</label>
							<label class="flex items-center gap-2 text-sm">
								<input type="checkbox" bind:checked={transport.smtpUseTls} />
								Use TLS/STARTTLS
							</label>
						</div>
					{/if}
				</section>
			{/each}
		</div>
	{/if}
</div>
