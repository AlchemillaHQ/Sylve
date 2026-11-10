<script lang="ts">
	import { OIDC_CALLBACK_PATH } from '$lib/api/auth';
	import {
		createRealmUser,
		deleteRealm,
		discoverRealm,
		listRealms,
		setRealm
	} from '$lib/api/auth/permissions';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { ACLGroup, AuthRealm, RealmDiscovery } from '$lib/types/auth';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import type { CellComponent } from 'tabulator-tables';
	import { onMount, tick, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { realms: AuthRealm[]; groups: ACLGroup[]; loadErrors: APIResponse[] } } =
		$props();
	let realms = $state(untrack(() => data.realms));
	let groupOptions = $derived(
		data.groups.map((group) => ({ label: group.name, value: group.name }))
	);
	let callbackURL = $derived(`${window.location.origin}${OIDC_CALLBACK_PATH}`);

	const items = (values: string[]) => values.map((value) => ({ label: value, value }));
	const usernameClaimOptions = items(['preferred_username', 'email', 'sub', 'nickname', 'name']);
	const groupsClaimOptions = items(['groups', 'roles']);
	const promptOptions = [
		{ label: 'login', value: 'login', description: 'Ask for credentials every time' },
		{ label: 'consent', value: 'consent', description: 'Ask for consent every time' },
		{
			label: 'select_account',
			value: 'select_account',
			description: 'Let the user pick an account'
		},
		{ label: 'none', value: 'none', description: "Never show the provider's pages" }
	];

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	async function refresh() {
		const result = await listRealms();
		if (isAPIResponse(result)) {
			handleAPIError(result);
			return;
		}
		realms = result;
	}

	function newRealm(): AuthRealm {
		return {
			name: '',
			label: '',
			issuer: '',
			clientId: '',
			hasSecret: false,
			scopes: 'openid profile email groups',
			usernameClaim: 'preferred_username',
			groupsClaim: 'groups',
			groupMappings: [],
			autoCreate: false,
			queryUserInfo: true,
			prompt: '',
			acrValues: '',
			hostedDomains: '',
			sessionHours: 8,
			caCert: '',
			default: false,
			enabled: true
		};
	}

	function editorFor(realm: AuthRealm, editing: boolean) {
		return {
			open: true,
			editing,
			tab: 'provider',
			realm: { ...realm },
			secret: '',
			publicClient: editing && !realm.hasSecret,
			scopes: { open: false, value: realm.scopes.split(/\s+/).filter(Boolean) },
			usernameClaim: { open: false, value: realm.usernameClaim },
			groupsClaim: { open: false, value: realm.groupsClaim },
			prompt: { open: false, value: realm.prompt },
			acrValues: { open: false, value: realm.acrValues.split(/\s+/).filter(Boolean) },
			hostedDomains: { open: false, value: realm.hostedDomains.split(/\s+/).filter(Boolean) },
			mappings: realm.groupMappings.map((m) => ({
				claim: m.claim,
				group: { open: false, value: m.group }
			})),
			discovery: null as RealmDiscovery | null
		};
	}

	function emptyUser() {
		return { open: false, username: '', fullName: '', email: '', notes: '' };
	}

	let editor = $state({ ...editorFor(newRealm(), false), open: false });
	let newUser = $state(emptyUser());
	let deleteOpen = $state(false);
	let saving = $state(false);
	let discoveryPanel = $state<HTMLElement>();

	let scopeOptions = $derived(
		items(editor.discovery?.scopesSupported ?? ['openid', 'profile', 'email', 'groups'])
	);
	let acrOptions = $derived(items(editor.discovery?.acrValuesSupported ?? []));
	let discoveredLists = $derived(
		editor.discovery
			? [
					{ title: 'Scopes', values: editor.discovery.scopesSupported ?? [] },
					{ title: 'Claims', values: editor.discovery.claimsSupported ?? [] },
					{ title: 'ACR values', values: editor.discovery.acrValuesSupported ?? [] }
				]
			: []
	);

	async function copyCallbackURL() {
		await navigator.clipboard.writeText(callbackURL);
		toast.success('Redirect URI copied', { position: 'bottom-center' });
	}

	function stateLine(icon: string, label: string): string {
		return `<span class="inline-flex items-center gap-1"><span class="${icon} h-3.5 w-3.5 shrink-0"></span><span>${label}</span></span>`;
	}

	let tableData = $derived.by((): { rows: Row[]; columns: Column[] } => {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'state',
				title: 'State',
				formatter: (cell: CellComponent) => {
					const { enabled, isDefault } = cell.getRow().getData();
					return `<div class="flex flex-col gap-1">${
						enabled
							? stateLine('icon-[mdi--check-circle] text-green-500', 'Enabled')
							: stateLine('icon-[mdi--close-circle] text-red-500', 'Disabled')
					}${
						isDefault
							? stateLine('icon-[mdi--star] text-amber-400', 'Default')
							: stateLine('icon-[mdi--star-outline] text-muted-foreground', 'Not default')
					}</div>`;
				}
			},
			{ field: 'name', title: 'Name' },
			{ field: 'label', title: 'Label' },
			{ field: 'issuer', title: 'Issuer' },
			{ field: 'clientId', title: 'Client ID' },
			{ field: 'autoCreate', title: 'Auto-create' },
			{ field: 'mappings', title: 'Group mappings', formatter: 'textarea' }
		];
		const rows: Row[] = realms.map((realm) => ({
			id: realm.name,
			state: `${realm.enabled ? 'Enabled' : 'Disabled'} ${realm.default ? 'Default' : ''}`,
			enabled: realm.enabled,
			isDefault: realm.default,
			name: realm.name,
			label: realm.label,
			issuer: realm.issuer,
			clientId: realm.clientId,
			autoCreate: realm.autoCreate ? 'Yes' : 'No',
			mappings: realm.groupMappings.map((m) => `${m.claim} → ${m.group}`).join('\n') || 'None'
		}));
		return { columns, rows };
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRealm = $derived(realms.find((realm) => realm.name === activeRows?.[0]?.id) ?? null);

	async function onDiscover() {
		const result = await discoverRealm(editor.realm.issuer.trim(), editor.realm.caCert);
		if (isAPIResponse(result)) {
			handleAPIError(result);
			toast.error('Discovery failed', { position: 'bottom-center' });
			return;
		}
		editor.discovery = result;
		await tick();
		discoveryPanel?.scrollIntoView({ behavior: 'smooth', block: 'start' });
	}

	async function onSave() {
		const realm: AuthRealm = {
			...editor.realm,
			name: editor.realm.name.trim(),
			issuer: editor.realm.issuer.trim(),
			clientId: editor.realm.clientId.trim(),
			scopes: editor.scopes.value.join(' '),
			usernameClaim: editor.usernameClaim.value,
			groupsClaim: editor.groupsClaim.value,
			prompt: editor.prompt.value,
			acrValues: editor.acrValues.value.join(' '),
			hostedDomains: editor.hostedDomains.value.join(' '),
			sessionHours: Number(editor.realm.sessionHours),
			groupMappings: editor.mappings
				.map((m) => ({ claim: m.claim.trim(), group: m.group.value }))
				.filter((m) => m.claim && m.group)
		};
		if (!realm.name || !realm.issuer || !realm.clientId) {
			toast.error('Name, issuer and client ID are required', { position: 'bottom-center' });
			editor.tab = 'provider';
			return;
		}
		if (!realm.usernameClaim) {
			toast.error('A username claim is required', { position: 'bottom-center' });
			editor.tab = 'sign-in';
			return;
		}
		let secret: string | undefined = editor.secret || undefined;
		if (editor.publicClient) {
			secret = '';
		}

		saving = true;
		try {
			const result = await setRealm(realm, secret);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to save realm', { position: 'bottom-center' });
				return;
			}
			toast.success('Realm saved', { position: 'bottom-center' });
			editor.open = false;
			await refresh();
		} finally {
			saving = false;
		}
	}

	async function onCreateUser() {
		if (!activeRealm || !newUser.username.trim()) {
			toast.error('Username is required', { position: 'bottom-center' });
			return;
		}
		saving = true;
		try {
			const result = await createRealmUser(activeRealm.name, {
				username: newUser.username.trim(),
				fullName: newUser.fullName,
				email: newUser.email,
				notes: newUser.notes
			});
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error('Failed to add user', { position: 'bottom-center' });
				return;
			}
			toast.success('User added; their first sign-in binds them', {
				position: 'bottom-center'
			});
			newUser = emptyUser();
		} finally {
			saving = false;
		}
	}
</script>

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => (editor = editorFor(newRealm(), false))}
			size="sm"
			class="h-6"
			disabled={saving}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if activeRealm}
			<Button
				onclick={() => activeRealm && (editor = editorFor(activeRealm, true))}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving}
			>
				<SpanWithIcon
					icon="icon-[material-symbols--edit]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit"
				/>
			</Button>
			<Button
				onclick={() => (newUser = { ...emptyUser(), open: true })}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving}
			>
				<SpanWithIcon icon="icon-[mdi--account-plus]" size="h-4 w-4" gap="gap-2" title="Add User" />
			</Button>
			<Button
				onclick={() => (deleteOpen = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={saving}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="tt-permission-realms"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

{#if editor.open}
	<Dialog.Root bind:open={editor.open}>
		<Dialog.Content
			class="flex h-[85vh] w-[90%] flex-col gap-0 overflow-hidden p-6 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-[720px]"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
			showCloseButton={!saving}
			onClose={() => (editor.open = false)}
		>
			<Dialog.Header class="shrink-0 pr-10">
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--login-variant]"
						size="h-5 w-5"
						gap="gap-2"
						title={editor.editing ? `Edit ${editor.realm.name}` : 'New OpenID Connect Realm'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			<Tabs.Root bind:value={editor.tab} class="mt-4 min-h-0 flex-1 gap-0">
				<Tabs.List class="grid w-full shrink-0 grid-cols-3 p-0">
					<Tabs.Trigger value="provider" class="border-b px-1 text-xs sm:text-sm">
						<span class="icon-[mdi--server-network-outline] h-4 w-4"></span>
						Provider
					</Tabs.Trigger>
					<Tabs.Trigger value="sign-in" class="border-b px-1 text-xs sm:text-sm">
						<span class="icon-[mdi--account-key-outline] h-4 w-4"></span>
						Sign-in
					</Tabs.Trigger>
					<Tabs.Trigger value="groups" class="border-b px-1 text-xs sm:text-sm">
						<span class="icon-[mdi--account-group-outline] h-4 w-4"></span>
						Group Mappings
					</Tabs.Trigger>
				</Tabs.List>

				<Tabs.Content value="provider" class="m-0 -mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-3">
					<div class="space-y-3">
						<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
							<CustomValueInput
								label="Name"
								placeholder="company"
								hint="Users sign in as name@realm; fixed once created"
								bind:value={editor.realm.name}
								classes="space-y-1.5"
								disabled={saving || editor.editing}
							/>
							<CustomValueInput
								label="Label"
								placeholder="Company SSO"
								hint="Shown on the login page"
								bind:value={editor.realm.label}
								classes="space-y-1.5"
								disabled={saving}
							/>
						</div>

						<CustomValueInput
							label="Issuer URL"
							placeholder="https://auth.example.com"
							hint={editor.editing ? 'Fixed: a new provider is a new realm' : ''}
							bind:value={editor.realm.issuer}
							classes="space-y-1.5"
							disabled={saving || editor.editing}
						/>

						<div class="space-y-5">
							<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
								<CustomValueInput
									label="Client ID"
									placeholder="sylve"
									bind:value={editor.realm.clientId}
									classes="space-y-1.5"
									disabled={saving || editor.editing}
								/>
								<CustomValueInput
									label="Client Secret"
									placeholder={editor.publicClient
										? 'Not used by a public client'
										: editor.editing && editor.realm.hasSecret
											? 'Unchanged'
											: 'From the provider'}
									type="password"
									bind:value={editor.secret}
									classes="space-y-1.5"
									disabled={saving || editor.publicClient}
								/>
							</div>
							<CustomCheckbox
								label="Public Client (No Secret)"
								bind:checked={editor.publicClient}
								disabled={saving}
							/>
						</div>

						<CustomValueInput
							label="CA Certificate"
							placeholder="Only for a provider behind a private CA (PEM)"
							type="textarea"
							textAreaClasses="min-h-20 max-h-44 overflow-y-auto font-mono text-xs"
							bind:value={editor.realm.caCert}
							classes="space-y-1.5"
							disabled={saving}
						/>

						<div class="space-y-1.5">
							<div class="flex h-7 items-center">
								<Label class="text-sm">Redirect URI</Label>
							</div>
							<div class="flex items-center gap-2">
								<code
									class="bg-muted min-w-0 flex-1 truncate rounded-md border px-3 py-2 font-mono text-xs"
									title={callbackURL}>{callbackURL}</code
								>
								<Button
									size="sm"
									variant="outline"
									class="h-8 shrink-0"
									title="Copy"
									onclick={() => copyCallbackURL()}
								>
									<span class="icon-[mdi--content-copy] h-4 w-4"></span>
								</Button>
							</div>
							<p class="text-muted-foreground text-xs">
								Register this at the provider. It's the address this browser reached Sylve at, so
								behind a reverse proxy it's the proxy's public address. Add the same path for every
								other address people sign in at.
							</p>
						</div>

						<div class="space-y-3 rounded-md border p-3" bind:this={discoveryPanel}>
							<div class="flex items-center justify-between gap-3">
								<div class="min-w-0">
									<p class="text-sm font-medium">Provider discovery</p>
									<p class="text-muted-foreground text-xs">
										Reads the issuer's configuration. Only a real sign-in can check the secret.
									</p>
								</div>
								<Button
									onclick={() => onDiscover()}
									size="sm"
									variant="outline"
									class="h-8 shrink-0"
									disabled={saving || !editor.realm.issuer.trim()}
								>
									<SpanWithIcon
										icon="icon-[mdi--magnify]"
										size="h-4 w-4"
										gap="gap-2"
										title="Discover"
									/>
								</Button>
							</div>
							{#if editor.discovery}
								<dl
									class="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 border-t pt-3 text-xs"
								>
									<dt class="text-muted-foreground">Authorization</dt>
									<dd class="font-mono break-all">{editor.discovery.authorizationEndpoint}</dd>
									<dt class="text-muted-foreground">Token</dt>
									<dd class="font-mono break-all">{editor.discovery.tokenEndpoint}</dd>
									<dt class="text-muted-foreground">UserInfo</dt>
									<dd class="font-mono break-all">
										{editor.discovery.userInfoEndpoint || 'None'}
									</dd>
									{#each discoveredLists as list (list.title)}
										<dt class="text-muted-foreground">{list.title}</dt>
										<dd class="flex flex-wrap gap-1">
											{#each list.values as value (value)}
												<span class="bg-secondary rounded px-1.5 py-0.5 font-mono">{value}</span>
											{:else}
												<span class="text-muted-foreground">Not listed</span>
											{/each}
										</dd>
									{/each}
								</dl>
							{/if}
						</div>
					</div>
				</Tabs.Content>

				<Tabs.Content value="sign-in" class="m-0 -mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-3">
					<div class="space-y-4">
						<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
							<CustomComboBox
								label="Scopes"
								placeholder="Select scopes"
								hint="openid is required; Discover lists what the provider supports"
								bind:open={editor.scopes.open}
								bind:value={editor.scopes.value}
								data={scopeOptions}
								multiple={true}
								allowCustom={true}
								width="w-full"
								classes="space-y-1.5 md:col-span-2"
								disabled={saving}
							/>
							<CustomComboBox
								label="Username Claim"
								placeholder="Select a claim"
								hint="Names new users and matches pre-created ones; existing users keep their name"
								bind:open={editor.usernameClaim.open}
								bind:value={editor.usernameClaim.value}
								data={usernameClaimOptions}
								allowCustom={true}
								disallowEmpty={true}
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
							<CustomComboBox
								label="Groups Claim"
								placeholder="Don't use the provider's groups"
								hint="A list of group names, or an object whose keys are the names (Zitadel roles)"
								bind:open={editor.groupsClaim.open}
								bind:value={editor.groupsClaim.value}
								data={groupsClaimOptions}
								allowCustom={true}
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
							<CustomComboBox
								label="Prompt"
								placeholder="Provider default"
								bind:open={editor.prompt.open}
								bind:value={editor.prompt.value}
								data={promptOptions}
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
							<CustomValueInput
								label="Session Hours"
								placeholder="8"
								type="number"
								hint="Changes at the provider are seen at the next sign-in"
								bind:value={editor.realm.sessionHours}
								classes="space-y-1.5"
								disabled={saving}
							/>
							<CustomComboBox
								label="ACR Values"
								placeholder="Not required"
								hint="The provider's ID token must carry one of these. Discover lists what it supports; leave empty for providers without acr"
								bind:open={editor.acrValues.open}
								bind:value={editor.acrValues.value}
								data={acrOptions}
								multiple={true}
								allowCustom={true}
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
							<CustomComboBox
								label="Hosted Domains"
								placeholder="Any domain"
								hint="Google: only accounts of these Workspace domains (the hd claim) may sign in. Type a domain to add it; leave empty for other providers"
								bind:open={editor.hostedDomains.open}
								bind:value={editor.hostedDomains.value}
								data={[]}
								multiple={true}
								allowCustom={true}
								width="w-full"
								classes="space-y-1.5"
								disabled={saving}
							/>
						</div>

						<div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
							<CustomCheckbox
								label="Enabled"
								bind:checked={editor.realm.enabled}
								disabled={saving}
							/>
							<CustomCheckbox
								label="Default realm on the login page"
								bind:checked={editor.realm.default}
								disabled={saving}
							/>
							<CustomCheckbox
								label="Create users on first sign-in"
								bind:checked={editor.realm.autoCreate}
								disabled={saving}
							/>
							<CustomCheckbox
								label="Query UserInfo"
								bind:checked={editor.realm.queryUserInfo}
								disabled={saving}
							/>
						</div>
					</div>
				</Tabs.Content>

				<Tabs.Content value="groups" class="m-0 -mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-3">
					<div class="space-y-3">
						<div class="flex items-center justify-between gap-2">
							<p class="text-muted-foreground text-sm">
								Members of a provider group join its permission group at their next sign-in.
							</p>
							<Button
								size="sm"
								variant="outline"
								class="h-6 shrink-0"
								disabled={saving}
								onclick={() =>
									editor.mappings.push({ claim: '', group: { open: false, value: '' } })}
							>
								<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="Add" />
							</Button>
						</div>
						{#each editor.mappings as mapping, index (index)}
							<div class="flex items-end gap-2">
								<CustomValueInput
									label="Provider Group"
									placeholder="infra-operators"
									bind:value={mapping.claim}
									classes="flex-1 space-y-1.5"
									disabled={saving}
								/>
								<CustomComboBox
									label="Permission Group"
									placeholder="Select a group"
									bind:open={mapping.group.open}
									bind:value={mapping.group.value}
									data={groupOptions}
									width="w-full"
									classes="flex-1 space-y-1.5"
									disabled={saving}
								/>
								<Button
									size="sm"
									variant="outline"
									class="h-9"
									disabled={saving}
									onclick={() => editor.mappings.splice(index, 1)}
								>
									<span class="icon-[mdi--delete] h-4 w-4"></span>
								</Button>
							</div>
						{:else}
							<div
								class="text-muted-foreground flex flex-col items-center justify-center gap-2 py-8 text-sm"
							>
								<span class="icon-[mdi--account-group-outline] h-10 w-10"></span>
								<span>No group mappings</span>
							</div>
						{/each}
					</div>
				</Tabs.Content>
			</Tabs.Root>

			<Dialog.Footer class="shrink-0 border-t pt-4">
				<Button onclick={() => onSave()} type="submit" size="sm" disabled={saving}>
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
{/if}

{#if newUser.open && activeRealm}
	<Dialog.Root bind:open={newUser.open}>
		<Dialog.Content
			class="sm:max-w-xl"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
			showCloseButton={!saving}
			onClose={() => (newUser = emptyUser())}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--account-plus]"
						size="h-5 w-5"
						gap="gap-2"
						title={`Add a ${activeRealm.label} user`}
					/>
				</Dialog.Title>
			</Dialog.Header>

			<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
				<CustomValueInput
					label="Full Name"
					placeholder="Alice Example"
					bind:value={newUser.fullName}
					classes="space-y-1.5"
					disabled={saving}
				/>
				<CustomValueInput
					label="Username"
					placeholder="alice"
					hint={`The value of the ${activeRealm.usernameClaim} claim; the first sign-in with it binds the account`}
					bind:value={newUser.username}
					classes="space-y-1.5"
					disabled={saving}
				/>
			</div>
			<CustomValueInput
				label="Email"
				placeholder="alice@example.com"
				bind:value={newUser.email}
				classes="space-y-1.5"
				disabled={saving}
			/>
			<CustomValueInput
				label="Notes"
				placeholder="Optional notes"
				type="textarea"
				textAreaClasses="min-h-24"
				bind:value={newUser.notes}
				classes="space-y-1.5"
				disabled={saving}
			/>

			<Dialog.Footer class="flex justify-end">
				<Button onclick={() => onCreateUser()} type="submit" size="sm" disabled={saving}>
					Add
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={deleteOpen}
	loading={saving}
	loadingLabel="Deleting..."
	keepOpenOnConfirm={true}
	customTitle={activeRealm
		? `Delete the realm ${activeRealm.name}? Its users and their grants are removed too.`
		: undefined}
	names={{ parent: 'realm', element: activeRealm?.name || '' }}
	actions={{
		onConfirm: async () => {
			if (saving || !activeRealm) return;
			saving = true;
			try {
				const result = await deleteRealm(activeRealm.name);
				if (result.status !== 'success') {
					handleAPIError(result);
					toast.error('Failed to delete realm', { position: 'bottom-center' });
					return;
				}
				toast.success('Realm deleted', { position: 'bottom-center' });
				activeRows = null;
				deleteOpen = false;
				await refresh();
			} finally {
				saving = false;
			}
		},
		onCancel: () => (deleteOpen = false)
	}}
></AlertDialog>
