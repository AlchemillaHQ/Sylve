<script lang="ts">
	import { updateNetworkGlobalConfig } from '$lib/api/network/global-config';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import {
		NetworkGlobalConfigUpdateSchema,
		type NetworkGlobalConfig
	} from '$lib/types/network/global-config';
	import { getAPIErrorText, handleAPIError } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(),
		reload = $bindable(false),
		config,
		node
	}: { open: boolean; reload?: boolean; config: NetworkGlobalConfig; node: string } = $props();

	const initial = untrack(() => config);
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});

	function createHostAlias() {
		return { id: generateNanoId(), address: '', hostname: '', aliases: '' };
	}

	function createForm() {
		const hostAliases = initial.hostAliases.map((alias) => ({
			id: generateNanoId(),
			address: alias.address,
			hostname: alias.hostname,
			aliases: alias.aliases.join(' ')
		}));
		return {
			dnsMode: initial.dnsMode as string,
			dnsServers: Array.from({ length: 3 }, (_, index) => initial.dnsServers[index] ?? ''),
			searchDomainsOverride: initial.searchDomainsOverride,
			searchDomains: [...initial.searchDomains],
			hostAliases: hostAliases.length ? hostAliases : [createHostAlias()]
		};
	}

	let form = $state(createForm());
	let tab = $state('dns');
	let dnsModeOpen = $state(false);
	let searchDomainsOpen = $state(false);
	let saving = $state(false);
	let error = $state('');

	function reset() {
		form = createForm();
		error = '';
	}

	async function save() {
		if (saving) return;
		error = '';
		const hostAliases = form.hostAliases.filter(
			(alias) => alias.address.trim() || alias.hostname.trim() || alias.aliases.trim()
		);
		const result = NetworkGlobalConfigUpdateSchema.safeParse({
			expectedRevision: initial.revision,
			dnsMode: form.dnsMode,
			dnsServers:
				form.dnsMode === 'manual'
					? form.dnsServers.map((value) => value.trim()).filter(Boolean)
					: [],
			searchDomainsOverride: form.searchDomainsOverride,
			searchDomains: form.searchDomainsOverride ? form.searchDomains : [],
			hostAliases: hostAliases.map((alias) => ({
				address: alias.address,
				hostname: alias.hostname,
				aliases: alias.aliases.trim().split(/\s+/).filter(Boolean)
			}))
		});
		if (!result.success) {
			const issue = result.error.issues[0];
			tab = issue.path[0] === 'hostAliases' ? 'hosts' : 'dns';
			let rowIndex = typeof issue.path[1] === 'number' ? issue.path[1] : -1;
			if (issue.path[0] === 'hostAliases' && rowIndex >= 0) {
				rowIndex = form.hostAliases.indexOf(hostAliases[rowIndex]);
			}
			error = rowIndex >= 0 ? `Entry ${rowIndex + 1}: ${issue.message}` : issue.message;
			return;
		}
		saving = true;
		try {
			const response = await updateNetworkGlobalConfig(result.data, { hostname: targetNode });
			if (!mounted) return;
			if (response.status !== 'success') {
				handleAPIError(response);
				error = friendlyError(getAPIErrorText(response));
				return;
			}
			toast.success('Global network configuration updated', { position: 'bottom-center' });
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}

	function friendlyError(code: string): string {
		switch (code) {
			case 'global_config_revision_conflict':
				return 'Settings changed while this dialog was open. Close it, refresh, and try again.';
			case 'global_resolver_not_resolvconf_managed':
				return 'The host resolver is managed outside resolvconf. Enable OS resolver management before applying DNS settings.';
			case 'global_host_alias_conflicts_with_unmanaged_entry':
				tab = 'hosts';
				return 'A hostname conflicts with an existing unmanaged /etc/hosts entry.';
			case 'duplicate_global_host_name':
				tab = 'hosts';
				return 'Each hostname must be unique within an address family. Separate IPv4 and IPv6 entries are allowed.';
			case 'invalid_global_dns_server':
				tab = 'dns';
				return 'Use unicast DNS server addresses. Link-local IPv6 addresses require an interface, such as fe80::1%bridge0.';
			default:
				return 'Unable to apply the configuration. Review the error details before trying again.';
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[85dvh] max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden sm:h-[32rem] sm:max-w-3xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => (open = false)}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
	>
		<Dialog.Header class="shrink-0">
			<Dialog.Title class="pr-14"
				><SpanWithIcon
					icon="icon-[mdi--server-network]"
					size="h-6 w-6"
					gap="gap-2"
					title="Update Global Configuration"
				/></Dialog.Title
			>
		</Dialog.Header>
		<Tabs.Root bind:value={tab} class="mt-4 min-h-0 flex-1">
			<Tabs.List class="grid w-full shrink-0 grid-cols-2">
				<Tabs.Trigger value="dns">DNS</Tabs.Trigger>
				<Tabs.Trigger value="hosts">Host Aliases</Tabs.Trigger>
			</Tabs.List>
			<Tabs.Content value="dns" class="min-h-0 flex-1 space-y-4 overflow-y-auto py-2 pr-1">
				<ComboBox
					label="DNS Mode"
					bind:open={dnsModeOpen}
					bind:value={form.dnsMode}
					data={[
						{ value: 'os', label: 'OS-managed' },
						{ value: 'manual', label: 'Manual' }
					]}
					disallowEmpty
					width="w-full"
					disabled={saving}
				/>
				{#if form.dnsMode === 'manual'}
					<div class="grid gap-3 sm:grid-cols-3">
						{#each form.dnsServers as _, index (index)}
							<CustomValueInput
								label={`DNS Server ${index + 1}`}
								placeholder={index === 0 ? '192.0.2.53' : 'Optional'}
								bind:value={form.dnsServers[index]}
								disabled={saving}
							/>
						{/each}
					</div>
				{/if}
				<CustomCheckbox
					label="Override DNS Search Domains"
					bind:checked={form.searchDomainsOverride}
					disabled={saving}
					classes="flex items-center gap-2"
				/>
				{#if form.searchDomainsOverride}
					<ComboBox
						label="Search Domains"
						bind:open={searchDomainsOpen}
						bind:value={form.searchDomains}
						data={[]}
						multiple
						allowCustom
						width="w-full"
						placeholder="Add a search domain"
						disabled={saving}
					/>
				{/if}
			</Tabs.Content>
			<Tabs.Content value="hosts" class="min-h-0 flex-1 space-y-3 overflow-y-auto py-2 pr-1">
				{#each form.hostAliases as alias, index (alias.id)}
					<div
						class="grid items-end gap-3 rounded-md border p-3 sm:grid-cols-[repeat(3,minmax(0,1fr))_auto]"
					>
						<CustomValueInput
							id={`${alias.id}-address`}
							label="IP Address"
							placeholder="192.0.2.10"
							bind:value={alias.address}
							classes="min-w-0 space-y-1.5"
							disabled={saving}
						/>
						<CustomValueInput
							id={`${alias.id}-hostname`}
							label="Hostname"
							placeholder="server.example.com"
							bind:value={alias.hostname}
							classes="min-w-0 space-y-1.5"
							disabled={saving}
						/>
						<CustomValueInput
							id={`${alias.id}-aliases`}
							label="Additional Aliases"
							placeholder="server files"
							bind:value={alias.aliases}
							classes="min-w-0 space-y-1.5"
							disabled={saving}
						/>
						<Button
							size="sm"
							variant="outline"
							class="h-9 justify-self-end"
							aria-label={`Remove host alias ${index + 1}`}
							disabled={saving}
							onclick={() => {
								const remaining = form.hostAliases.filter((row) => row.id !== alias.id);
								form.hostAliases = remaining.length ? remaining : [createHostAlias()];
							}}
						>
							<span
								class="icon-[mdi--trash-can-outline] text-destructive h-4 w-4"
								aria-hidden="true"
							></span>
						</Button>
					</div>
				{/each}
				<Button
					variant="outline"
					size="sm"
					disabled={saving || form.hostAliases.length >= 128}
					onclick={() => {
						form.hostAliases = [...form.hostAliases, createHostAlias()];
					}}>Add Host Alias</Button
				>
			</Tabs.Content>
		</Tabs.Root>
		{#if error}<p class="text-destructive shrink-0 pt-3 text-sm" role="alert">{error}</p>{/if}
		<Dialog.Footer class="shrink-0 pt-4">
			<Button size="sm" disabled={saving} aria-busy={saving} onclick={save}>
				{#if saving}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"
					></span>Saving...{:else}Save{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
