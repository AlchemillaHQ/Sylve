<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { listGroups } from '$lib/api/auth/groups';
	import { listUsers } from '$lib/api/auth/local';
	import { getNFSConfigResult, getNFSDatasetsResult, saveNFSShare } from '$lib/api/nfs';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { NFSConfig, NFSDataset, NFSShare, NFSShareInput } from '$lib/types/nfs';
	import { isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import DatasetPicker from '../DatasetPicker.svelte';
	import AccountPicker from './AccountPicker.svelte';
	import ClientsInput from './ClientsInput.svelte';
	import {
		accountProblem,
		applyPending,
		clientProblem,
		datasetChoiceReason,
		fieldError,
		mappingMode,
		pendingDetail,
		requestError,
		SECURITY_FLAVORS,
		type MappingMode,
		type Security
	} from './nfs';

	let {
		hostname,
		share,
		readOnly = false,
		onClose,
		onSaved
	}: {
		hostname: string;
		share?: NFSShare;
		readOnly?: boolean;
		onClose: () => void;
		onSaved: () => void;
	} = $props();

	type Draft = {
		dataset: string;
		description: string;
		enabled: boolean;
		readOnly: boolean;
		everyone: boolean;
		clients: string[];
		security: Security[];
		mode: MappingMode;
		user: string;
		group: string;
		allDirs: boolean;
	};
	type Choice<T extends string> = { value: T; label: string; icon: string; description: string };
	type Risk = 'root' | 'everyone';

	const ACCESS_LEVELS: Choice<'read' | 'write'>[] = [
		{
			value: 'read',
			label: 'Read Only',
			icon: 'icon-[lucide--eye]',
			description: 'Clients can read files but not change them'
		},
		{
			value: 'write',
			label: 'Read & Write',
			icon: 'icon-[lucide--pencil]',
			description: 'Clients can create, change and delete files'
		}
	];
	const CLIENT_MODES: Choice<'clients' | 'everyone'>[] = [
		{
			value: 'clients',
			label: 'Specific Clients',
			icon: 'icon-[lucide--network]',
			description: 'Only the addresses and networks you list'
		},
		{
			value: 'everyone',
			label: 'Everyone',
			icon: 'icon-[lucide--globe]',
			description: 'Any host that can reach this node'
		}
	];
	const MAPPING_MODES: Choice<MappingMode>[] = [
		{
			value: 'squash',
			label: 'Squash Root',
			icon: 'icon-[lucide--shield]',
			description: 'Root on clients acts as nobody; other users keep their IDs'
		},
		{
			value: 'root',
			label: 'Map Root',
			icon: 'icon-[lucide--user-cog]',
			description: 'Root on clients acts as the user you choose'
		},
		{
			value: 'all',
			label: 'Map Everyone',
			icon: 'icon-[lucide--users]',
			description: 'Every client user acts as the user you choose'
		}
	];
	const TABS = [
		{ value: 'general', label: 'General', icon: 'icon-[lucide--folder-cog]' },
		{ value: 'access', label: 'Access', icon: 'icon-[lucide--network]' },
		{ value: 'users', label: 'Users', icon: 'icon-[lucide--users]' }
	] as const;
	type Tab = (typeof TABS)[number]['value'];
	const TAB_FIELDS: Record<Tab, string[]> = {
		general: ['dataset', 'description'],
		access: ['clients', 'security'],
		users: ['user', 'group']
	};
	const SERVER_FIELDS: Record<string, string> = {
		dataset: 'dataset',
		description: 'description',
		clients: 'clients',
		security: 'security',
		kerberosPrincipal: 'security',
		numericIds: 'security',
		maprootUser: 'user',
		mapallUser: 'user',
		maprootGroup: 'group',
		mapallGroup: 'group'
	};
	const HIDDEN_BLOCKS = new Set(['sylve_data', 'boot_environment']);

	const id = $props.id();
	const lifetime = new AbortController();
	const target = untrack(() => share);
	const initial: Draft = {
		dataset: target?.dataset ?? '',
		description: target?.description ?? '',
		enabled: target?.enabled ?? true,
		readOnly: target?.readOnly ?? true,
		everyone: target?.everyone ?? true,
		clients: [...(target?.clients ?? [])],
		security: [...(target?.security ?? ['sys'])],
		mode: target ? mappingMode(target) : 'squash',
		user: (target?.mapallUser || target?.maprootUser) ?? '',
		group: (target?.mapallUser ? target.mapallGroup : target?.maprootGroup) ?? '',
		allDirs: target?.allDirs ?? false
	};

	let draft = $state<Draft>(structuredClone(initial));
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let serverErrors = $state<Record<string, { message: string; value: string }>>({});
	let tab = $state<Tab>('general');
	let confirming = $state<Risk[]>([]);
	let locked = $derived(saving || readOnly);

	let datasets = $state<NFSDataset[] | null>(null);
	let datasetsFailed = $state(false);
	let users = $state<string[] | null>(null);
	let groups = $state<string[] | null>(null);
	let config = $state<NFSConfig | null>(null);

	let selectedDataset = $derived(datasets?.find((entry) => entry.guid === draft.dataset));
	let title = $derived(
		(selectedDataset?.code === 'dataset_missing' ? '' : selectedDataset?.name) ||
			target?.datasetName ||
			target?.dataset ||
			''
	);
	let datasetChoices = $derived.by(() => {
		const entries = (datasets ?? [])
			.filter(
				(entry) => !entry.block || !HIDDEN_BLOCKS.has(entry.block) || entry.guid === draft.dataset
			)
			.map((entry) => {
				const own = Boolean(target) && entry.guid === target?.dataset;
				const missing = own && entry.code === 'dataset_missing';
				return {
					guid: entry.guid,
					name: (missing && target?.datasetName) || entry.name,
					mountpoint: (missing && target?.mountpoint) || entry.mountpoint,
					reason: own ? '' : datasetChoiceReason(entry)
				};
			});
		return [
			...entries.filter((entry) => !entry.reason),
			...entries.filter((entry) => entry.reason)
		];
	});
	let changed = $derived(
		!target || JSON.stringify(normalized(draft)) !== JSON.stringify(normalized(initial))
	);
	let policyChanged = $derived(
		!target ||
			JSON.stringify({ ...normalized(draft), description: '' }) !==
				JSON.stringify({ ...normalized(initial), description: '' })
	);
	let datasetChecked = $derived(
		!target || draft.dataset !== target.dataset || (draft.enabled && policyChanged)
	);
	let kerberos = $derived(draft.security.some((flavor) => flavor !== 'sys'));

	let problems = $derived<Record<string, string>>({
		dataset: datasetIssue(),
		description: descriptionProblem(draft.description),
		clients: clientsIssue(),
		security: securityIssue(),
		user:
			draft.mode === 'squash'
				? ''
				: draft.user.trim()
					? accountProblem(draft.user)
					: 'Choose a user',
		group: draft.mode === 'squash' ? '' : accountProblem(draft.group)
	});
	let valid = $derived(Object.values(problems).every((problem) => !problem));

	function datasetIssue(): string {
		if (!draft.dataset) return 'Choose a dataset';
		if (!datasets || !datasetChecked) return '';
		if (!selectedDataset) return 'It no longer exists';
		return datasetChoiceReason(selectedDataset);
	}

	function descriptionProblem(value: string): string {
		const trimmed = value.trim();
		if (new TextEncoder().encode(trimmed).length > 256) return 'Use at most 256 bytes';
		return /\p{Cc}/u.test(trimmed.replace(/[\r\n]/g, '')) ? 'Remove control characters' : '';
	}

	function clientsIssue(): string {
		if (draft.everyone) return '';
		if (draft.clients.length === 0) return 'Add an address, or choose Everyone';
		if (draft.clients.length > 64) return 'Use at most 64 addresses';
		return draft.clients.some((entry) => clientProblem(entry))
			? 'Fix the highlighted addresses'
			: '';
	}

	function securityIssue(): string {
		if (draft.security.length === 0) return 'Choose at least one';
		if (!kerberos || !draft.enabled || !policyChanged || !config) return '';
		if (config.numericIds && config.protocol !== 'v3') {
			return 'Turn off numeric IDs in NFS settings first';
		}
		return config.kerberosPrincipal ? '' : 'Choose a Kerberos principal in NFS settings first';
	}

	function normalized(value: Draft): Draft {
		const mapped = value.mode !== 'squash';
		return {
			...value,
			description: value.description.trim(),
			clients: value.everyone ? [] : [...value.clients].sort(),
			security: [...value.security].sort(),
			user: mapped ? value.user.trim() : '',
			group: mapped ? value.group.trim() : ''
		};
	}

	function fieldValue(key: string): string {
		switch (key) {
			case 'dataset':
				return draft.dataset;
			case 'description':
				return draft.description;
			case 'clients':
				return JSON.stringify([draft.everyone, draft.clients]);
			case 'security':
				return JSON.stringify(draft.security);
			case 'user':
				return JSON.stringify([draft.mode, draft.user, draft.group]);
			case 'group':
				return JSON.stringify([draft.mode, draft.group]);
		}
		return '';
	}

	function problem(key: string): string {
		const server = serverErrors[key];
		if (server && server.value === fieldValue(key)) return server.message;
		const value = problems[key] ?? '';
		if (!value || attempted) return value;
		switch (key) {
			case 'dataset':
				return draft.dataset ? value : '';
			case 'clients':
				return touched.clients && draft.clients.length ? value : '';
			case 'security':
				return draft.security.length ? value : '';
			case 'user':
				return touched.user && draft.user.trim() ? value : '';
			case 'group':
				return touched.group && draft.group.trim() ? value : '';
			case 'description':
				return touched.description && draft.description.trim() ? value : '';
		}
		return '';
	}

	function tabHasError(value: Tab): boolean {
		return TAB_FIELDS[value].some((key) => problem(key));
	}

	function risks(value: Draft): Risk[] {
		const current = normalized(value);
		const found: Risk[] = [];
		if (current.mode !== 'squash' && ['root', '0'].includes(current.user)) found.push('root');
		if (current.everyone && !current.readOnly) found.push('everyone');
		return found;
	}

	function toggleSecurity(flavor: Security, checked: boolean) {
		draft.security = checked
			? [...draft.security, flavor]
			: draft.security.filter((entry) => entry !== flavor);
	}

	async function loadDatasets() {
		datasetsFailed = false;
		const result = await getNFSDatasetsResult({ hostname, signal: lifetime.signal }).catch(
			() => null
		);
		if (lifetime.signal.aborted) return;
		if (!result || isAPIResponse(result)) datasetsFailed = true;
		else datasets = result;
	}

	async function loadAccounts() {
		const [userList, groupList] = await Promise.all([
			listUsers('pam', { hostname, signal: lifetime.signal }).catch(() => null),
			listGroups({ hostname, signal: lifetime.signal }).catch(() => null)
		]);
		if (lifetime.signal.aborted) return;
		const names = (list: string[], extra: string[]) =>
			[...new Set([...list, ...extra])].sort((a, b) => a.localeCompare(b));
		users = names(Array.isArray(userList) ? userList.map((user) => user.username) : [], ['nobody']);
		groups = names(Array.isArray(groupList) ? groupList.map((group) => group.name) : [], [
			'nobody',
			'nogroup'
		]);
	}

	async function loadConfig() {
		const result = await getNFSConfigResult({ hostname, signal: lifetime.signal }).catch(
			() => null
		);
		if (!lifetime.signal.aborted && result && !isAPIResponse(result)) config = result;
	}

	void loadDatasets();
	void loadAccounts();
	void loadConfig();
	onDestroy(() => lifetime.abort());

	function reset() {
		if (locked) return;
		draft = structuredClone(initial);
		touched = {};
		attempted = false;
		saveError = '';
		serverErrors = {};
	}

	function input(): NFSShareInput {
		const value = normalized(draft);
		return {
			dataset: value.dataset,
			description: value.description,
			enabled: value.enabled,
			readOnly: value.readOnly,
			everyone: value.everyone,
			clients: value.clients,
			maprootUser: value.mode === 'root' ? value.user : '',
			maprootGroup: value.mode === 'root' ? value.group : '',
			mapallUser: value.mode === 'all' ? value.user : '',
			mapallGroup: value.mode === 'all' ? value.group : '',
			allDirs: value.allDirs,
			security: value.security
		};
	}

	function submit(event: SubmitEvent) {
		event.preventDefault();
		if (readOnly) return;
		attempted = true;
		if (saving || !valid || !changed) {
			if (!tabHasError(tab)) {
				const next = TABS.find((entry) => tabHasError(entry.value));
				if (next) tab = next.value;
			}
			return;
		}
		const accepted = risks(initial);
		const danger = risks(draft).filter((entry) => !accepted.includes(entry));
		if (danger.length) {
			confirming = danger;
			return;
		}
		void save();
	}

	async function save() {
		confirming = [];
		saving = true;
		saveError = '';
		const name = title;
		try {
			const response = await saveNFSShare(target?.id ?? null, input(), {
				hostname,
				signal: lifetime.signal
			});
			if (lifetime.signal.aborted) return;
			if (response.status === 'success') {
				if (applyPending(response)) {
					toast.warning(pendingDetail(response), { position: 'bottom-center' });
				} else {
					toast.success(target ? `Share of ${name} saved` : `Share of ${name} created`, {
						position: 'bottom-center'
					});
				}
				onSaved();
				onClose();
				return;
			}
			const detail = fieldError(response);
			const key = detail ? SERVER_FIELDS[detail.field] : undefined;
			if (detail && key) {
				serverErrors[key] = { message: detail.message, value: fieldValue(key) };
				const owner = TABS.find((entry) => TAB_FIELDS[entry.value].includes(key));
				if (owner) tab = owner.value;
			} else {
				saveError = requestError(response, 'The share couldn’t be saved.');
			}
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = 'The share couldn’t be saved.';
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

{#snippet hint(label: string, description: string)}
	{@const helpId = `${id}-${label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}-help`}
	<span id={helpId} class="sr-only">{description}</span>
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {label}"
			aria-describedby={helpId}
			class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
		>
			<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
		</Tooltip.Trigger>
		<Tooltip.Content
			side="top"
			align="end"
			sideOffset={6}
			class="z-60 max-w-64 text-left leading-5"
		>
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet label(key: string, text: string, description?: string)}
	{@const error = problem(key)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<Label for="{id}-{key}">{text}</Label>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{#if description}{@render hint(text, description)}{/if}
	</div>
{/snippet}

{#snippet heading(key: string, text: string, description: string)}
	{@const error = problem(key)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<h3 id="{id}-{key}-heading" class="text-sm font-medium">{text}</h3>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{@render hint(text, description)}
	</div>
{/snippet}

{#snippet cards(
	key: string,
	value: string,
	choices: Choice<string>[],
	onChange: (value: string) => void,
	columns: string
)}
	<RadioGroup.Root
		{value}
		onValueChange={onChange}
		disabled={locked}
		class="grid gap-2 {columns}"
		aria-labelledby="{id}-{key}-heading"
	>
		{#each choices as choice (choice.value)}
			<label
				for="{id}-{key}-{choice.value}"
				class="group has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-muted/40 flex min-w-0 cursor-pointer flex-col gap-2 rounded-lg border p-3 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-70"
			>
				<span class="flex items-center justify-between gap-2">
					<span
						class="bg-muted text-muted-foreground group-has-[[data-state=checked]]:bg-zen-highlight group-has-[[data-state=checked]]:text-zen-highlight-foreground flex size-8 items-center justify-center rounded-md"
						aria-hidden="true"
					>
						<span class="{choice.icon} size-4"></span>
					</span>
					<RadioGroup.Item value={choice.value} id="{id}-{key}-{choice.value}" />
				</span>
				<span class="min-w-0 space-y-1">
					<span class="block text-sm leading-tight font-medium">{choice.label}</span>
					<span class="text-muted-foreground block text-xs leading-snug">{choice.description}</span>
				</span>
			</label>
		{/each}
	</RadioGroup.Root>
{/snippet}

{#snippet checkRow(
	key: string,
	text: string,
	checked: boolean,
	onChange: (checked: boolean) => void,
	description: string
)}
	<div class="flex items-center justify-between gap-2">
		<div class="flex items-center gap-2">
			<Checkbox
				id="{id}-{key}"
				{checked}
				disabled={locked}
				onCheckedChange={(value) => onChange(value === true)}
			/>
			<Label for="{id}-{key}">{text}</Label>
		</div>
		{@render hint(text, description)}
	</div>
{/snippet}

{#snippet scroller(text: string, content: Snippet)}
	<ScrollArea
		class="h-full"
		fade
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'overscroll-contain',
			role: 'region',
			'aria-label': text,
			tabindex: 0
		}}
	>
		<div class="px-4 pb-4">{@render content()}</div>
	</ScrollArea>
{/snippet}

{#snippet datasetPicker()}
	{@const error = problem('dataset')}
	<div class="min-w-0 space-y-2">
		{@render label(
			'dataset',
			'Dataset',
			'The ZFS file system to export. The root file system, system folders, boot environments and Sylve’s own data can’t be exported.'
		)}
		<DatasetPicker
			id="{id}-dataset"
			value={draft.dataset}
			label={title}
			placeholder={datasets === null && !datasetsFailed ? 'Loading datasets…' : 'Choose a dataset'}
			choices={datasetChoices}
			loading={datasets === null}
			failed={datasetsFailed}
			disabled={locked}
			invalid={Boolean(error)}
			describedBy={error ? `${id}-dataset-error` : undefined}
			onRetry={() => void loadDatasets()}
			onChange={(guid) => (draft.dataset = guid)}
		/>
	</div>
{/snippet}

{#snippet general()}
	<div class="space-y-5">
		<div class="grid gap-4">
			{@render datasetPicker()}
			<div class="min-w-0 space-y-2">
				{@render label(
					'description',
					'Description',
					'A note shown next to the dataset in Sylve. Clients don’t see it.'
				)}
				<Textarea
					id="{id}-description"
					bind:value={draft.description}
					placeholder="Backups for the build servers"
					rows={3}
					disabled={locked}
					onblur={() => (touched.description = true)}
					aria-invalid={problem('description') ? true : undefined}
					aria-describedby={problem('description') ? `${id}-description-error` : undefined}
				/>
			</div>
		</div>
		<section class="space-y-3" aria-labelledby="{id}-level-heading">
			{@render heading(
				'level',
				'Access Level',
				'What clients can do with the files. File permissions still apply on top of this.'
			)}
			{@render cards(
				'level',
				draft.readOnly ? 'read' : 'write',
				ACCESS_LEVELS,
				(value) => (draft.readOnly = value === 'read'),
				'sm:grid-cols-2'
			)}
		</section>
		<div class="grid gap-x-6 gap-y-3 sm:grid-cols-2">
			{@render checkRow(
				'enabled',
				'Enabled',
				draft.enabled,
				(value) => (draft.enabled = value),
				'A disabled share keeps its settings but isn’t exported.'
			)}
			{@render checkRow(
				'allDirs',
				'Allow Mounting Subfolders',
				draft.allDirs,
				(value) => (draft.allDirs = value),
				'Lets NFSv3 clients mount any folder inside the dataset, not just its top folder.'
			)}
		</div>
	</div>
{/snippet}

{#snippet access()}
	{@const clientsError = problem('clients')}
	{@const securityError = problem('security')}
	<div class="space-y-5">
		<section class="space-y-3" aria-labelledby="{id}-clients-heading">
			{@render heading(
				'clients',
				'Who Can Connect',
				'NFS trusts clients by address. List the hosts and networks that may mount this share.'
			)}
			{@render cards(
				'clients',
				draft.everyone ? 'everyone' : 'clients',
				CLIENT_MODES,
				(value) => (draft.everyone = value === 'everyone'),
				'sm:grid-cols-2'
			)}
			{#if !draft.everyone}
				<div class="space-y-2">
					<Label for="{id}-clients-input">Addresses and Networks</Label>
					<ClientsInput
						id="{id}-clients-input"
						value={draft.clients}
						disabled={locked}
						invalid={Boolean(clientsError)}
						describedBy={clientsError ? `${id}-clients-error` : undefined}
						onChange={(next) => (draft.clients = next)}
						onBlur={() => (touched.clients = true)}
					/>
				</div>
			{/if}
		</section>
		<section class="space-y-3" aria-labelledby="{id}-security-heading">
			{@render heading(
				'security',
				'Security',
				'How clients prove who they are. Kerberos types need a Kerberos principal in NFS settings.'
			)}
			<div
				class="grid gap-2 sm:grid-cols-2"
				role="group"
				aria-labelledby="{id}-security-heading"
				aria-describedby={securityError ? `${id}-security-error` : undefined}
			>
				{#each SECURITY_FLAVORS as flavor (flavor.value)}
					<label
						for="{id}-security-{flavor.value}"
						class="has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-muted/40 flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-70"
					>
						<Checkbox
							id="{id}-security-{flavor.value}"
							class="mt-0.5"
							checked={draft.security.includes(flavor.value)}
							disabled={locked}
							onCheckedChange={(value) => toggleSecurity(flavor.value, value === true)}
						/>
						<span class="min-w-0 space-y-1">
							<span class="block font-mono text-sm leading-tight font-medium">{flavor.label}</span>
							<span class="text-muted-foreground block text-xs leading-snug"
								>{flavor.description}</span
							>
						</span>
					</label>
				{/each}
			</div>
		</section>
	</div>
{/snippet}

{#snippet mapping()}
	<div class="space-y-5">
		<section class="space-y-3" aria-labelledby="{id}-mapping-heading">
			{@render heading(
				'mapping',
				'User Mapping',
				'Which user client requests act as on this node. Squashing root stops clients’ root users from acting as root here.'
			)}
			{@render cards(
				'mapping',
				draft.mode,
				MAPPING_MODES,
				(value) => (draft.mode = value as MappingMode),
				'sm:grid-cols-3'
			)}
		</section>
		{#if draft.mode !== 'squash'}
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="min-w-0 space-y-2">
					{@render label(
						'user',
						draft.mode === 'root' ? 'Root Acts As' : 'Everyone Acts As',
						'A user on this node, or a directory account such as user@example.com.'
					)}
					<AccountPicker
						id="{id}-user"
						kind="user"
						value={draft.user}
						options={users ?? []}
						loading={users === null}
						disabled={locked}
						invalid={Boolean(problem('user'))}
						describedBy={problem('user') ? `${id}-user-error` : undefined}
						onChange={(value) => {
							draft.user = value;
							touched.user = true;
						}}
					/>
				</div>
				<div class="min-w-0 space-y-2">
					{@render label(
						'group',
						'Group',
						'Optional. Without a group, the user’s own groups are used.'
					)}
					<AccountPicker
						id="{id}-group"
						kind="group"
						value={draft.group}
						options={groups ?? []}
						loading={groups === null}
						optional
						disabled={locked}
						invalid={Boolean(problem('group'))}
						describedBy={problem('group') ? `${id}-group-error` : undefined}
						onChange={(value) => {
							draft.group = value;
							touched.group = true;
						}}
					/>
				</div>
			</div>
		{/if}
	</div>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:h-[min(40rem,90dvh)] sm:max-w-4xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		interactOutsideBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={submit} novalidate>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{readOnly
								? 'icon-[lucide--eye]'
								: target
									? 'icon-[lucide--pencil]'
									: 'icon-[lucide--plus]'} text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">
							{target ? (readOnly ? title : `Edit Share — ${title}`) : 'New Share'}
						</span>
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						{readOnly
							? `The settings of the NFS share of ${title}.`
							: target
								? `Change the settings of the NFS share of ${title}.`
								: 'Export a ZFS file system over NFS.'}
					</Dialog.Description>
				</Dialog.Header>
				<Dialog.Close disabled={saving}>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost"
							size="icon"
							class="size-9 shrink-0"
							disabled={saving}
						>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</Dialog.Close>
			</div>
			<Tabs.Root bind:value={tab} class="min-h-0 min-w-0 flex-1 gap-0">
				<div class="shrink-0 px-4 pt-4">
					<Tabs.List class="mb-4 w-full">
						{#each TABS as entry (entry.value)}
							{@const error = tabHasError(entry.value)}
							<Tabs.Trigger
								value={entry.value}
								class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm"
								aria-label={error ? `${entry.label}: has errors` : undefined}
							>
								<span class="{entry.icon} size-3.5" aria-hidden="true"></span>{entry.label}
								{#if error}
									<span class="bg-destructive size-1.5 shrink-0 rounded-full" aria-hidden="true"
									></span>
								{/if}
							</Tabs.Trigger>
						{/each}
					</Tabs.List>
				</div>
				<Tabs.Content value="general" class="mt-0 min-h-0" tabindex={-1}>
					{@render scroller('General', general)}
				</Tabs.Content>
				<Tabs.Content value="access" class="mt-0 min-h-0" tabindex={-1}>
					{@render scroller('Access', access)}
				</Tabs.Content>
				<Tabs.Content value="users" class="mt-0 min-h-0" tabindex={-1}>
					{@render scroller('Users', mapping)}
				</Tabs.Content>
			</Tabs.Root>
			{#if saveError}
				<p class="text-destructive shrink-0 border-t px-4 py-3 text-sm break-words" role="alert">
					{saveError}
				</p>
			{/if}
			<div class="flex shrink-0 items-center justify-between gap-3 border-t p-4">
				{#if readOnly}
					<p class="text-muted-foreground text-sm">You can view this share but not change it.</p>
					<Dialog.Close>
						{#snippet child({ props })}
							<Button {...props} variant="outline" class="h-9">Close</Button>
						{/snippet}
					</Dialog.Close>
				{:else}
					<Button
						type="button"
						variant="outline"
						class="h-9"
						disabled={saving || (Boolean(target) && !changed)}
						onclick={reset}
					>
						Reset
					</Button>
					<Button
						type="submit"
						class="h-9 gap-2"
						disabled={saving || (Boolean(target) && !changed) || (attempted && !valid)}
					>
						{#if saving}
							<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
							></span>
						{/if}
						{#if target}
							{saving ? 'Saving…' : 'Save Changes'}
						{:else}
							{saving ? 'Creating…' : 'Create Share'}
						{/if}
					</Button>
				{/if}
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>

{#if confirming.length}
	{@const root = confirming.includes('root')}
	{@const everyone = confirming.includes('everyone')}
	<AlertDialog.Root
		open
		onOpenChange={(open) => {
			if (!open) confirming = [];
		}}
	>
		<AlertDialog.Content>
			<AlertDialog.Header class="text-left">
				<AlertDialog.Title>
					{root && everyone
						? 'Give Everyone Root Access?'
						: root
							? 'Give Clients Root Access?'
							: 'Let Everyone Change Files?'}
				</AlertDialog.Title>
				<AlertDialog.Description class="text-pretty">
					{#if everyone}
						Any host that can reach this node can mount this share and create, change or delete its
						files.
					{/if}
					{#if root}
						Requests from clients will act as root on this node, so client users can read and change
						every file in the dataset.
					{/if}
				</AlertDialog.Description>
			</AlertDialog.Header>
			<AlertDialog.Footer>
				<AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
				<AlertDialog.Action onclick={() => void save()}>Save Anyway</AlertDialog.Action>
			</AlertDialog.Footer>
		</AlertDialog.Content>
	</AlertDialog.Root>
{/if}
