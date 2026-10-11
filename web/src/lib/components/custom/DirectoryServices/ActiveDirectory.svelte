<script lang="ts">
	import {
		updateActiveDirectory,
		setADEnabled,
		checkADStatus,
		discoverActiveDirectory
	} from '$lib/api/directory-services';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type {
		ActiveDirectory,
		ActiveDirectoryInput,
		IDMap,
		KerberosRealm,
		KerberosKeytab
	} from '$lib/types/directory-services';
	import { getAPIErrorText } from '$lib/utils/http';
	import { formatDirectoryServiceError } from '$lib/utils/directory-services';
	import { reportAPIError } from '$lib/stores/error-details.svelte';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(),
		ad,
		node,
		realms,
		keytabs,
		reload = $bindable(false)
	}: {
		open: boolean;
		ad: ActiveDirectory;
		node: string;
		realms: KerberosRealm[];
		keytabs: KerberosKeytab[];
		reload?: boolean;
	} = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
		joinPassword = '';
	});
	function initialForm(): ActiveDirectoryInput {
		return {
			domain: ad.domain,
			workgroup: ad.workgroup,
			computerName: ad.computerName,
			site: ad.site,
			computerOU: ad.computerOU,
			domainController: ad.domainController,
			realmId: ad.realmId,
			keytabId: ad.keytabId,
			kerberosPrincipal: ad.kerberosPrincipal,
			joinUsername: ad.joinUsername,
			allowTrustedDomains: ad.allowTrustedDomains,
			useDefaultDomain: ad.useDefaultDomain,
			disableCache: ad.disableCache,
			skipDNSRegistration: ad.skipDNSRegistration,
			idmapBackend: ad.idmapBackend,
			rangeLow: ad.rangeLow,
			rangeHigh: ad.rangeHigh,
			idmapSchema: ad.idmapSchema,
			trustedDomains: ad.trustedDomains.map((mapping) => ({ ...mapping }))
		};
	}
	let form = $state(initialForm());
	let joinPassword = $state('');
	let clearJoinPassword = $state(false);
	let canKeepJoinPassword = $derived(
		ad.joinPasswordConfigured &&
			form.joinUsername.trim() === ad.joinUsername &&
			form.domain.trim().toUpperCase() === ad.domain
	);
	let enabled = $state(untrack(() => ad.enabled));
	const enabledId = $props.id();
	let activeTab = $state('details');
	let error = $state('');
	let saving = $state(false);
	let discovering = $state(false);
	let discoveryMessage = $state('');
	let realmOpen = $state(false);
	let keytabOpen = $state(false);
	let principalOpen = $state(false);
	let realmValue = $state(untrack(() => String(ad.realmId || '')));
	let keytabValue = $state(untrack(() => String(ad.keytabId || '')));
	let authentication = $state(untrack(() => (ad.keytabId ? 'keytab' : 'password')));
	let editingMapping = $state<number | null>(null);
	let mapping = $state<IDMap>({
		domain: '',
		backend: 'rid',
		rangeLow: 2000000000,
		rangeHigh: 2099999999,
		schema: 'rfc2307'
	});
	let mappingOpen = $state(false);
	const backends = [
		{ value: 'rid', label: 'RID' },
		{ value: 'ad', label: 'AD / RFC2307' },
		{ value: 'autorid', label: 'AUTORID' }
	];
	const schemas = [
		{ value: 'rfc2307', label: 'RFC2307' },
		{ value: 'sfu', label: 'SFU' },
		{ value: 'sfu20', label: 'SFU20' }
	];
	const selectClasses = {
		parent: 'flex min-w-0 flex-col gap-1.5',
		label: 'flex h-7 items-center whitespace-nowrap text-sm',
		trigger: 'flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 py-1 text-left'
	};
	let realmOptions = $derived([
		{ value: '', label: 'Automatic (domain realm)' },
		...realms.map((realm) => ({ value: String(realm.id), label: realm.realm }))
	]);
	let keytabOptions = $derived(
		keytabs
			.filter((keytab) => !keytab.managed)
			.map((keytab) => ({ value: String(keytab.id), label: keytab.name }))
	);
	let principalOptions = $derived(
		(keytabs.find((keytab) => keytab.id === Number(keytabValue))?.principals ?? []).map(
			(principal) => ({ value: principal, label: principal })
		)
	);
	let addMappingDisabledReason = $derived(
		ad.membershipPending
			? 'Finish or recover the pending membership operation before adding a mapping.'
			: saving
				? 'Wait for the current save to finish.'
				: form.idmapBackend === 'autorid'
					? 'AUTORID assigns domain ranges automatically; manual mappings are not supported.'
					: ''
	);

	function reset() {
		form = initialForm();
		joinPassword = '';
		clearJoinPassword = false;
		enabled = ad.enabled;
		realmValue = String(ad.realmId || '');
		keytabValue = String(ad.keytabId || '');
		authentication = ad.keytabId ? 'keytab' : 'password';
		error = '';
		discoveryMessage = '';
		activeTab = 'details';
		mappingOpen = false;
	}
	async function discover() {
		if (saving || discovering) return;
		if (!form.domain.trim()) {
			error = 'Enter a domain to discover.';
			return;
		}
		discovering = true;
		error = '';
		const input = {
			domain: form.domain.trim(),
			site: form.site.trim(),
			domainController: form.domainController.trim()
		};
		try {
			const result = await discoverActiveDirectory(input, { hostname: targetNode });
			if (
				!mounted ||
				!open ||
				input.domain !== form.domain.trim() ||
				input.site !== form.site.trim() ||
				input.domainController !== form.domainController.trim()
			)
				return;
			// Discovery fills blanks only. Explicit administrator choices are retained.
			if (!form.workgroup.trim()) form.workgroup = result.workgroup;
			discoveryMessage =
				`Discovered workgroup ${result.workgroup}${result.site ? ` and site ${result.site}` : ''} through ${result.domainController}. ${result.warning}`.trim();
			if (form.workgroup.trim().toUpperCase() !== result.workgroup)
				error = `The configured workgroup does not match the discovered NetBIOS name ${result.workgroup}.`;
		} catch {
			if (mounted && open)
				error = 'Domain discovery failed. Check the domain, DNS and controller connectivity.';
		} finally {
			discovering = false;
		}
	}
	function addMapping() {
		if (!mapping.domain.trim() || mapping.rangeHigh <= mapping.rangeLow) {
			toast.error('A domain and valid ID range are required.', { position: 'bottom-center' });
			return;
		}
		const entry = { ...mapping, domain: mapping.domain.trim().toUpperCase() };
		if (
			form.trustedDomains.some(
				(item, index) => index !== editingMapping && item.domain.toUpperCase() === entry.domain
			)
		) {
			toast.error('A mapping for this domain already exists.', { position: 'bottom-center' });
			return;
		}
		if (editingMapping === null) form.trustedDomains = [...form.trustedDomains, entry];
		else
			form.trustedDomains = form.trustedDomains.map((item, i) =>
				i === editingMapping ? entry : item
			);
		mappingOpen = false;
	}
	async function save() {
		if (saving || discovering) return;
		error = '';
		if (!form.domain.trim() || !form.computerName.trim()) {
			activeTab = 'details';
			error = 'Domain and computer name are required.';
			return;
		}
		if (authentication === 'keytab' && (!keytabValue || !form.kerberosPrincipal)) {
			activeTab = 'authentication';
			error = 'Select a keytab and principal.';
			return;
		}
		if (
			authentication === 'password' &&
			((joinPassword && !form.joinUsername.trim()) ||
				(form.joinUsername.trim() && !joinPassword && !clearJoinPassword && !canKeepJoinPassword))
		) {
			activeTab = 'authentication';
			error = 'Enter the join username and password.';
			return;
		}
		if (
			!Number.isInteger(form.rangeLow) ||
			!Number.isInteger(form.rangeHigh) ||
			form.rangeLow < 1000000 ||
			form.rangeHigh <= form.rangeLow
		) {
			activeTab = 'options';
			error = 'ID ranges must be whole numbers from 1000000, with high greater than low.';
			return;
		}
		saving = true;
		const desiredEnabled = enabled;
		const enabledChanged = desiredEnabled !== ad.enabled;
		try {
			const result = await updateActiveDirectory(
				{
					...form,
					allowTrustedDomains:
						form.idmapBackend === 'autorid'
							? form.allowTrustedDomains
							: form.trustedDomains.length > 0,
					realmId: Number(realmValue),
					keytabId: authentication === 'keytab' ? Number(keytabValue) : 0,
					kerberosPrincipal: authentication === 'keytab' ? form.kerberosPrincipal : '',
					joinUsername: authentication === 'password' ? form.joinUsername.trim() : '',
					joinPassword: authentication === 'password' && joinPassword ? joinPassword : undefined,
					clearJoinPassword: authentication === 'password' && clearJoinPassword
				},
				{ hostname: targetNode }
			);
			if (!mounted) return;
			if (result.status === 'error') {
				error = formatDirectoryServiceError(getAPIErrorText(result));
				reportAPIError(result, error);
				return;
			}
			joinPassword = '';
			clearJoinPassword = false;
			let connectionCheckFailed = false;
			if (enabledChanged) {
				const result = await setADEnabled(desiredEnabled, { hostname: targetNode });
				if (!mounted) return;
				if (result.status === 'error') {
					error = `Configuration saved, but ${desiredEnabled ? 'enabling' : 'disabling'} failed. ${formatDirectoryServiceError(getAPIErrorText(result))}`;
					reportAPIError(result, error);
					activeTab = 'details';
					reload = true;
					return;
				}
				if (desiredEnabled) {
					try {
						await checkADStatus({ hostname: targetNode });
					} catch {
						connectionCheckFailed = true;
					}
					if (!mounted) return;
				}
			}
			if (connectionCheckFailed)
				toast.warning(
					'Configuration saved, but the connection check failed. Use Check Connection to retry.',
					{
						position: 'bottom-center'
					}
				);
			else toast.success('Active Directory configuration updated.', { position: 'bottom-center' });
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden p-5 sm:h-[82dvh] sm:max-h-[42rem] sm:w-[calc(100vw-4rem)] sm:max-w-4xl! sm:p-6"
		showCloseButton={!saving && !discovering}
		showResetButton={!saving && !discovering}
		onEscapeKeydown={(event) => {
			if (saving || discovering) event.preventDefault();
		}}
		onReset={reset}
		onClose={() => (open = false)}
	>
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--server-network]"
					size="h-5 w-5"
					gap="gap-2"
					title="Update Active Directory Configuration"
				/></Dialog.Title
			>
			<Dialog.Description class="mt-1 text-xs"
				>Configure this node's domain membership and identity mapping. Joining is a separate action.</Dialog.Description
			>
		</Dialog.Header>
		<Tabs.Root bind:value={activeTab} class="mt-4 min-h-0 flex-1 gap-0">
			<Tabs.List class="grid w-full shrink-0 grid-cols-3 p-0">
				<Tabs.Trigger value="details" class="border-b px-1 text-xs sm:text-sm"
					><span class="icon-[mdi--server-network] h-4 w-4"></span>Details</Tabs.Trigger
				>
				<Tabs.Trigger value="authentication" class="border-b px-1 text-xs sm:text-sm"
					><span class="icon-[mdi--account-key-outline] h-4 w-4"></span>Authentication</Tabs.Trigger
				>
				<Tabs.Trigger value="options" class="border-b px-1 text-xs sm:text-sm"
					><span class="icon-[mdi--tune-variant] h-4 w-4"></span>Options</Tabs.Trigger
				>
			</Tabs.List>
			{#if error}<div
					class="border-destructive/40 bg-destructive/10 text-destructive mt-3 rounded-md border px-3 py-2 text-sm"
					role="alert"
				>
					{error}
				</div>{/if}
			<Tabs.Content value="details" class="m-0 min-h-0 flex-1 overflow-y-auto py-2">
				<div class="space-y-5 pb-1">
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<CustomValueInput
							label="Domain"
							placeholder="example.com"
							bind:value={form.domain}
							classes="space-y-1.5"
							disabled={ad.joined || saving}
						/>
						<CustomValueInput
							label="Workgroup"
							placeholder="Automatic discovery"
							bind:value={form.workgroup}
							classes="space-y-1.5"
							disabled={ad.joined || saving}
							hint="The AD domain's NetBIOS name. Leave blank to discover it when saving."
						/>
						<CustomValueInput
							label="Computer Name"
							placeholder="SYLVE"
							bind:value={form.computerName}
							classes="space-y-1.5"
							disabled={ad.joined || saving}
							hint="Unique within the domain; at most 15 characters."
						/>
						<ComboBox
							label="Kerberos Realm"
							bind:open={realmOpen}
							bind:value={realmValue}
							data={realmOptions}
							placeholder="Automatic (domain realm)"
							width="w-full"
							classes="space-y-1.5"
							disabled={ad.joined || saving}
						/>
					</div>
					{#if !ad.joined}
						<div class="space-y-2">
							<Button
								size="sm"
								variant="outline"
								disabled={saving || discovering || !form.domain.trim()}
								onclick={discover}
							>
								<span
									class={discovering
										? 'icon-[mdi--loading] size-4 animate-spin'
										: 'icon-[mdi--magnify] size-4'}
								></span>
								{discovering ? 'Discovering...' : 'Discover Domain'}
							</Button>
							{#if discoveryMessage}<p
									class="text-muted-foreground break-words text-xs"
									role="status"
								>
									{discoveryMessage}
								</p>{/if}
						</div>
					{/if}
					<div class="flex items-center space-x-3 rounded-lg border border-border bg-muted/40 p-4">
						<Checkbox
							id={enabledId}
							bind:checked={enabled}
							disabled={saving || !ad.joined || !!ad.membershipPending}
							aria-describedby={`${enabledId}-hint`}
							class="border-muted-foreground/40 bg-background"
						/>
						<div class="grid gap-1.5 leading-none">
							<Label for={enabledId} class="text-sm font-medium leading-none">Enabled</Label>
							<p id={`${enabledId}-hint`} class="text-[10px] italic text-muted-foreground">
								{ad.joined
									? 'Disabling stops domain access without leaving the domain. Applied when you save.'
									: 'Join the domain before enabling integration.'}
							</p>
						</div>
					</div>
				</div>
			</Tabs.Content>
			<Tabs.Content value="authentication" class="m-0 min-h-0 flex-1 overflow-y-auto py-2">
				<div class="grid gap-4 pb-1">
					<SimpleSelect
						label="Join Authentication"
						classes={selectClasses}
						bind:value={authentication}
						options={[
							{ value: 'password', label: 'Username and password' },
							{ value: 'keytab', label: 'Kerberos keytab' }
						]}
						onChange={() => {
							joinPassword = '';
							clearJoinPassword = false;
						}}
						disabled={saving}
					/>
					{#if authentication === 'keytab'}
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<ComboBox
								label="Keytab"
								bind:open={keytabOpen}
								bind:value={keytabValue}
								data={keytabOptions}
								placeholder="Select uploaded keytab"
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
								onValueChange={() => (form.kerberosPrincipal = '')}
							/>
							<ComboBox
								label="Principal"
								bind:open={principalOpen}
								bind:value={form.kerberosPrincipal}
								data={principalOptions}
								placeholder="Select principal"
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
						</div>
					{:else}
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<CustomValueInput
								label="Username"
								placeholder="joinuser or joinuser@EXAMPLE.COM"
								bind:value={form.joinUsername}
								classes="space-y-1.5"
								disabled={saving}
								autocomplete="off"
							/>
							<CustomValueInput
								label="Password"
								placeholder={canKeepJoinPassword
									? 'Leave blank to keep saved password'
									: 'Join password'}
								type="password"
								bind:value={joinPassword}
								classes="space-y-1.5"
								disabled={saving || clearJoinPassword}
								autocomplete="new-password"
							/>
						</div>
						{#if ad.joinPasswordConfigured}
							<CustomCheckbox
								label="Clear Saved Password"
								bind:checked={clearJoinPassword}
								disabled={saving || !!joinPassword}
							/>
						{/if}
					{/if}
					<section class="space-y-3">
						<div class="flex items-center justify-between gap-2">
							<p class="text-sm font-semibold">Trusted Domain Mappings</p>
							<span class="inline-flex" title={addMappingDisabledReason || undefined}>
								<Button
									size="sm"
									variant="outline"
									disabled={!!addMappingDisabledReason}
									title={addMappingDisabledReason || 'Add a trusted domain mapping'}
									onclick={() => {
										editingMapping = null;
										mapping = {
											domain: '',
											backend: 'rid',
											rangeLow: 2000000000,
											rangeHigh: 2099999999,
											schema: 'rfc2307'
										};
										mappingOpen = true;
									}}><span class="icon-[mdi--plus] h-4 w-4"></span>Add Mapping</Button
								>
							</span>
						</div>
						{#if form.idmapBackend === 'autorid'}
							<div class="space-y-2">
								<CustomCheckbox
									label="Allow Trusted Domains"
									bind:checked={form.allowTrustedDomains}
									disabled={saving}
								/>
								<p class="text-muted-foreground text-xs">
									AUTORID maps trusted domains automatically. Remove any manual mappings before
									saving with this backend.
								</p>
							</div>
						{/if}
						<div class="overflow-x-auto rounded-md border">
							<table class="w-full text-sm">
								<thead class="bg-muted/30"
									><tr
										><th class="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
											>Domain</th
										><th class="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
											>Backend</th
										><th class="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
											>Range</th
										><th class="px-3 py-2"><span class="sr-only">Actions</span></th></tr
									></thead
								><tbody
									>{#each form.trustedDomains as item, i (i)}
										{@const mappingLocked =
											ad.joined && ad.trustedDomains.some((saved) => saved.domain === item.domain)}
										<tr class="border-t hover:bg-muted/20"
											><td class="px-3 py-2">{item.domain}</td><td class="px-3 py-2"
												>{item.backend.toUpperCase()}</td
											><td class="px-3 py-2 font-mono text-xs">{item.rangeLow}–{item.rangeHigh}</td
											><td class="px-3 py-2"
												><Button
													size="icon"
													variant="ghost"
													class="h-7 w-7"
													title="Edit mapping"
													disabled={mappingLocked || saving || form.idmapBackend === 'autorid'}
													onclick={() => {
														editingMapping = i;
														mapping = { ...item };
														mappingOpen = true;
													}}><span class="icon-[mdi--pencil-outline] size-3.5"></span></Button
												><Button
													size="icon"
													variant="ghost"
													class="h-7 w-7 text-destructive"
													title="Remove mapping"
													disabled={mappingLocked || saving}
													onclick={() =>
														(form.trustedDomains = form.trustedDomains.filter(
															(_, index) => index !== i
														))}><span class="icon-[mdi--trash-can-outline] size-3.5"></span></Button
												></td
											></tr
										>{:else}<tr
											><td colspan="4" class="px-3 py-2 text-center text-xs text-muted-foreground"
												>No trusted domain mappings configured</td
											></tr
										>{/each}</tbody
								>
							</table>
						</div>
						{#if mappingOpen && form.idmapBackend !== 'autorid'}<div
								class="space-y-3 rounded-md border p-4"
							>
								<div class="grid gap-4">
									<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
										<CustomValueInput
											label="Domain NetBIOS Name"
											placeholder="TRUSTED"
											bind:value={mapping.domain}
										/>
										<SimpleSelect
											label="Backend"
											classes={selectClasses}
											bind:value={mapping.backend}
											options={backends.filter((backend) => backend.value !== 'autorid')}
											onChange={() => {}}
										/>
									</div>
									<div
										class="grid grid-cols-1 gap-4 {mapping.backend === 'ad'
											? 'sm:grid-cols-3'
											: 'sm:grid-cols-2'}"
									>
										<CustomValueInput
											label="Range Low"
											placeholder="2000000000"
											type="number"
											bind:value={mapping.rangeLow}
										/>
										<CustomValueInput
											label="Range High"
											placeholder="2099999999"
											type="number"
											bind:value={mapping.rangeHigh}
										/>
										{#if mapping.backend === 'ad'}
											<SimpleSelect
												label="Schema"
												classes={selectClasses}
												bind:value={mapping.schema}
												options={schemas}
												onChange={() => {}}
											/>
										{/if}
									</div>
								</div>
								<div class="flex justify-end gap-2">
									<Button size="sm" variant="outline" onclick={() => (mappingOpen = false)}
										>Cancel</Button
									><Button size="sm" onclick={addMapping}>Apply Mapping</Button>
								</div>
							</div>{/if}
					</section>
				</div>
			</Tabs.Content>
			<Tabs.Content value="options" class="m-0 min-h-0 flex-1 overflow-y-auto py-2">
				<div class="space-y-5 pb-1">
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
						<CustomValueInput
							label="AD Site"
							placeholder="Automatic discovery"
							bind:value={form.site}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Preferred Domain Controller"
							placeholder="Automatic discovery"
							bind:value={form.domainController}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Computer Account OU"
							placeholder="Computers/Servers"
							bind:value={form.computerOU}
							classes="space-y-1.5"
							hint="Samba OU path, separated by /, not an LDAP distinguished name. Used when joining."
						/>
					</div>
					<div class="flex flex-wrap items-center gap-x-6 gap-y-3">
						<CustomCheckbox
							label="Use Unqualified Usernames"
							bind:checked={form.useDefaultDomain}
						/>
						<CustomCheckbox
							label="Disable Directory Enumeration and Cache"
							bind:checked={form.disableCache}
						/>
						<CustomCheckbox
							label="Skip AD DNS Registration"
							bind:checked={form.skipDNSRegistration}
							disabled={saving}
							title="Used when joining. Manage the member's DNS record in AD separately when skipped."
						/>
					</div>
					<section class="space-y-3">
						<p class="text-sm font-semibold">Identity mapping</p>
						<Card.Root class="border-blue-500/30 bg-blue-500/10 p-0!" role="note">
							<Card.Content
								class="flex items-start gap-2 p-3 text-sm text-blue-600 dark:text-blue-400"
							>
								<span
									class="icon-[material-symbols--info-outline] mt-0.5 h-4 w-4 shrink-0"
									aria-hidden="true"
								></span>
								<p class="min-w-0">
									Mappings must stay consistent across nodes. Domain and existing mappings are
									locked while joined or referenced by any saved share. New trusted-domain mappings
									can be added without disabling AD or leaving the domain. RID derives IDs from
									domain RIDs. AD requires directory Unix attributes. AUTORID allocates domain
									ranges; preserve its mapping database when restoring or moving storage.
								</p>
							</Card.Content>
						</Card.Root>
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
							<SimpleSelect
								label="Backend"
								classes={selectClasses}
								bind:value={form.idmapBackend}
								options={backends}
								onChange={() => {}}
								disabled={ad.joined || saving}
							/>
							<CustomValueInput
								label="Range Low"
								placeholder="1000000"
								type="number"
								bind:value={form.rangeLow}
								classes="space-y-1.5"
								disabled={ad.joined || saving}
							/>
							<CustomValueInput
								label="Range High"
								placeholder="1999999999"
								type="number"
								bind:value={form.rangeHigh}
								classes="space-y-1.5"
								disabled={ad.joined || saving}
							/>
							{#if form.idmapBackend === 'ad'}<SimpleSelect
									label="Schema"
									classes={selectClasses}
									bind:value={form.idmapSchema}
									options={schemas}
									onChange={() => {}}
									disabled={ad.joined || saving}
								/>{/if}
						</div>
					</section>
				</div>
			</Tabs.Content>
		</Tabs.Root>
		<Dialog.Footer class="shrink-0 border-t pt-4"
			><Button onclick={save} disabled={saving || discovering}
				>{#if saving}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"
					></span>Saving...{:else}Save Changes{/if}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
