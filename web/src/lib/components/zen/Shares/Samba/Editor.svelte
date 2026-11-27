<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getActiveDirectory, searchDirectoryPrincipals } from '$lib/api/directory-services';
	import { getSambaConfig } from '$lib/api/samba/config';
	import { getSambaPrincipals, getShareDatasets, saveSambaShare } from '$lib/api/samba/share';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { DirectoryPrincipal } from '$lib/types/directory-services';
	import type {
		SambaPrincipals,
		SambaShare,
		SambaShareInput,
		ShareDataset
	} from '$lib/types/samba/shares';
	import { isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { Debounced, watch } from 'runed';
	import { onDestroy, untrack, type Snippet } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { toast } from 'svelte-sonner';
	import DatasetPicker from '../DatasetPicker.svelte';
	import PrincipalPicker from './PrincipalPicker.svelte';
	import { datasetBlockReason } from '../shared';
	import {
		accessMode,
		auditOperationLabel,
		datasetChoiceBlock,
		extraConfigProblem,
		maskProblem,
		MAX_SHARE_NAME_LENGTH,
		nameProblem,
		shareSaveError,
		wholeNumberProblem,
		type AccessMode,
		type PrincipalOption
	} from './samba';

	let {
		hostname,
		share,
		shares,
		readOnly = false,
		onClose,
		onSaved
	}: {
		hostname: string;
		share?: SambaShare;
		shares: SambaShare[];
		readOnly?: boolean;
		onClose: () => void;
		onSaved: () => Promise<unknown> | void;
	} = $props();

	type Draft = {
		name: string;
		dataset: string;
		enabled: boolean;
		mode: AccessMode;
		read: string[];
		write: string[];
		createMask: string;
		directoryMask: string;
		timeMachine: boolean;
		timeMachineMaxSize: string;
		auditEnabled: boolean;
		auditRetentionDays: string;
		auditedOperations: string[];
		extraShareConfig: string;
	};

	const ACCESS_MODES: { value: AccessMode; label: string; icon: string; description: string }[] = [
		{
			value: 'signed-in',
			label: 'Signed-In Users',
			icon: 'icon-[lucide--users]',
			description: 'Only the users and groups you choose'
		},
		{
			value: 'guest-read',
			label: 'Guests Read Only',
			icon: 'icon-[lucide--eye]',
			description: 'Anyone on the network can read files'
		},
		{
			value: 'guest-write',
			label: 'Guests Read & Write',
			icon: 'icon-[lucide--globe]',
			description: 'Anyone on the network can change files'
		}
	];
	const DEFAULT_AUDIT = ['connect', 'disconnect', 'mkdirat', 'unlinkat', 'renameat', 'write'];
	const AUDIT_GROUPS = [
		{
			key: 'sessions',
			label: 'Sessions',
			description: 'Clients connecting and leaving',
			operations: ['connect', 'disconnect']
		},
		{
			key: 'changes',
			label: 'Changes',
			description: 'Creating, writing, renaming, deleting',
			operations: ['create_file', 'mkdirat', 'write', 'renameat', 'unlinkat']
		},
		{
			key: 'reads',
			label: 'Reads',
			description: 'Opening and reading; logs a lot',
			operations: ['openat', 'read', 'close']
		}
	];
	const TABS = [
		{ value: 'general', label: 'General', icon: 'icon-[lucide--folder-cog]' },
		{ value: 'access', label: 'Access', icon: 'icon-[lucide--users]' },
		{ value: 'audit', label: 'Audit', icon: 'icon-[lucide--scroll-text]' },
		{ value: 'advanced', label: 'Advanced', icon: 'icon-[lucide--sliders-horizontal]' }
	] as const;
	type Tab = (typeof TABS)[number]['value'];
	const TAB_FIELDS: Record<Tab, string[]> = {
		general: ['name', 'dataset', 'timeMachineMaxSize'],
		access: ['access'],
		audit: ['auditedOperations', 'auditRetentionDays'],
		advanced: ['createMask', 'directoryMask', 'extraShareConfig']
	};

	const id = $props.id();
	const lifetime = new AbortController();
	const target = untrack(() => share);
	const others = untrack(() => shares.filter((entry) => entry.id !== target?.id));
	const savedDirectory = target
		? [...target.permissions.read.directory, ...target.permissions.write.directory]
		: [];
	const initial: Draft = {
		name: target?.name ?? '',
		dataset: target?.dataset ?? '',
		enabled: target?.enabled ?? true,
		mode: target ? accessMode(target) : 'signed-in',
		read: target ? principalKeys(target.permissions.read) : [],
		write: target ? principalKeys(target.permissions.write) : [],
		createMask: target?.createMask ?? '0664',
		directoryMask: target?.directoryMask ?? '2775',
		timeMachine: target?.timeMachine ?? false,
		timeMachineMaxSize: String(target?.timeMachineMaxSize ?? 0),
		auditEnabled: target?.auditEnabled ?? false,
		auditRetentionDays: String(target?.auditRetentionDays ?? 70),
		auditedOperations: [...(target?.auditedOperations ?? [])],
		extraShareConfig: target?.extraShareConfig ?? ''
	};

	let draft = $state<Draft>(structuredClone(initial));
	let applyExisting = $state(false);
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let tab = $state<Tab>('general');
	let locked = $derived(saving || readOnly);

	let datasets = $state<ShareDataset[] | null>(null);
	let datasetsFailed = $state(false);
	let principals = $state<SambaPrincipals | null>(null);
	let principalsFailed = $state(false);
	let appleExtensions = $state<boolean | null>(null);
	let configFailed = $state(false);
	let directoryEnabled = $state(false);
	let directoryExact = $state(false);
	let directoryResults = $state<DirectoryPrincipal[]>([]);
	let searchQuery = $state('');
	let searching = $state(false);
	let searchError = $state('');
	let searchController: AbortController | undefined;
	const completedSearches = new SvelteSet<string>();
	const debouncedQuery = new Debounced(() => searchQuery, 400);

	function principalKeys(set: SambaShare['permissions']['read']): string[] {
		return [
			...set.users.map((user) => `u:${user.id}`),
			...set.groups.map((group) => `g:${group.id}`),
			...set.directory.map((principal) => `ad:${principal.sid}`)
		];
	}

	let options = $derived.by(() => {
		const list: PrincipalOption[] = [];
		const seen: Record<string, true> = {};
		const add = (option: PrincipalOption) => {
			if (seen[option.key]) return;
			seen[option.key] = true;
			list.push(option);
		};
		for (const user of principals?.users ?? []) {
			add({
				key: `u:${user.id}`,
				label: user.username,
				kind: 'user',
				directory: false,
				warning:
					user.sambaAccount === false ? 'No Samba password, so they can’t sign in' : undefined
			});
		}
		for (const group of principals?.groups ?? []) {
			add({ key: `g:${group.id}`, label: group.name, kind: 'group', directory: false });
		}
		for (const set of target ? [target.permissions.read, target.permissions.write] : []) {
			for (const user of set.users) {
				add({ key: `u:${user.id}`, label: user.username, kind: 'user', directory: false });
			}
			for (const group of set.groups) {
				add({ key: `g:${group.id}`, label: group.name, kind: 'group', directory: false });
			}
		}
		for (const principal of [...savedDirectory, ...directoryResults]) {
			add({
				key: `ad:${principal.sid}`,
				label: principal.name,
				kind: principal.kind,
				directory: true
			});
		}
		return list;
	});
	let readNotes = $derived(Object.fromEntries(draft.write.map((key) => [key, 'Has read & write'])));
	let writeNotes = $derived(Object.fromEntries(draft.read.map((key) => [key, 'Has read only'])));

	let selectedDataset = $derived(datasets?.find((entry) => entry.guid === draft.dataset));
	let datasetChoices = $derived.by(() => {
		const entries = (datasets ?? [])
			.filter((entry) => entry.block !== 'sylve_data' || entry.guid === draft.dataset)
			.map((entry) => ({
				guid: entry.guid,
				name: entry.name,
				mountpoint: entry.mountpoint,
				reason: choiceBlock(entry)
			}));
		return [
			...entries.filter((entry) => !entry.reason),
			...entries.filter((entry) => entry.reason)
		];
	});
	let datasetIssue = $derived.by(() => {
		if (!draft.dataset) return 'Choose a dataset';
		if (!datasets) return '';
		const own = Boolean(target) && draft.dataset === target?.dataset;
		if (!selectedDataset) return own && !draft.enabled ? '' : 'It no longer exists';
		if (!own) return datasetChoiceBlock(selectedDataset, target?.id);
		if (!draft.enabled) return '';
		if (!selectedDataset.mounted) return 'It isn’t mounted';
		if (selectedDataset.block && !target?.enabled) return datasetBlockReason(selectedDataset.block);
		return '';
	});

	let problems = $derived<Record<string, string>>({
		name: nameProblem(draft.name, others),
		dataset: datasetIssue,
		access:
			draft.mode === 'signed-in' && draft.read.length + draft.write.length === 0
				? 'Choose at least one user or group'
				: '',
		createMask: maskProblem(draft.createMask),
		directoryMask: maskProblem(draft.directoryMask),
		timeMachineMaxSize: draft.timeMachine
			? wholeNumberProblem(draft.timeMachineMaxSize, 'Enter a whole number of GiB')
			: '',
		auditRetentionDays: draft.auditEnabled
			? wholeNumberProblem(draft.auditRetentionDays, 'Enter a whole number of days')
			: '',
		auditedOperations:
			draft.auditEnabled && draft.auditedOperations.length === 0 ? 'Choose at least one' : '',
		extraShareConfig: extraConfigProblem(draft.extraShareConfig)
	});
	let valid = $derived(Object.values(problems).every((problem) => !problem));
	let changed = $derived(
		!target || JSON.stringify(normalized(draft)) !== JSON.stringify(normalized(initial))
	);
	let canSave = $derived(changed || applyExisting);
	let timeMachineBlock = $derived(
		appleExtensions
			? ''
			: configFailed
				? 'Samba settings couldn’t be loaded'
				: appleExtensions === null
					? 'Checking Samba settings'
					: 'Turn on Apple extensions in Samba settings first'
	);

	function normalized(value: Draft): Draft {
		return {
			...value,
			name: value.name.trim(),
			read: value.mode === 'signed-in' ? [...value.read].sort() : [],
			write: value.mode === 'signed-in' ? [...value.write].sort() : [],
			auditedOperations: [...value.auditedOperations].sort()
		};
	}

	function choiceBlock(entry: ShareDataset): string {
		if (target && entry.guid === target.dataset) return '';
		return datasetChoiceBlock(entry, target?.id);
	}

	function problem(key: string): string {
		const value = problems[key] ?? '';
		if (attempted) return value;
		if (key === 'dataset') return draft.dataset ? value : '';
		const current = draft[key as keyof Draft];
		return touched[key] && typeof current === 'string' && current.trim() ? value : '';
	}

	function tabHasError(value: Tab): boolean {
		return TAB_FIELDS[value].some((key) => problem(key));
	}

	function toggle(list: 'read' | 'write', key: string) {
		const other = list === 'read' ? 'write' : 'read';
		if (draft[list].includes(key)) {
			draft[list] = draft[list].filter((entry) => entry !== key);
		} else {
			draft[list] = [...draft[list], key];
			draft[other] = draft[other].filter((entry) => entry !== key);
		}
	}

	function toggleGroup(operations: string[], checked: boolean) {
		const others = draft.auditedOperations.filter((value) => !operations.includes(value));
		draft.auditedOperations = checked ? [...others, ...operations] : others;
	}

	function toggleOperation(value: string, checked: boolean) {
		draft.auditedOperations = checked
			? [...draft.auditedOperations, value]
			: draft.auditedOperations.filter((entry) => entry !== value);
	}

	async function loadDatasets() {
		datasetsFailed = false;
		const result = await getShareDatasets({ hostname, signal: lifetime.signal }).catch(() => null);
		if (lifetime.signal.aborted) return;
		if (!result || isAPIResponse(result)) datasetsFailed = true;
		else datasets = result;
	}

	async function loadPrincipals() {
		principalsFailed = false;
		const result = await getSambaPrincipals({ hostname, signal: lifetime.signal }).catch(
			() => null
		);
		if (lifetime.signal.aborted) return;
		if (!result || isAPIResponse(result)) principalsFailed = true;
		else principals = result;
	}

	async function loadConfig() {
		try {
			const config = await getSambaConfig({ hostname, signal: lifetime.signal });
			if (!lifetime.signal.aborted) appleExtensions = config.appleExtensions;
		} catch (error) {
			if (!lifetime.signal.aborted && !isRequestCancellation(error)) configFailed = true;
		}
	}

	async function loadDirectory() {
		try {
			const ad = await getActiveDirectory({ hostname, signal: lifetime.signal });
			if (lifetime.signal.aborted) return;
			directoryEnabled = ad.enabled && ad.joined;
			directoryExact = ad.disableCache;
			if (directoryEnabled && !directoryExact) void searchDirectory('');
		} catch {
			directoryEnabled = false;
		}
	}

	async function searchDirectory(query: string) {
		const trimmed = query.trim();
		searchController?.abort();
		searchController = undefined;
		searching = false;
		searchError = '';
		if (!directoryEnabled || (trimmed && trimmed.length < 2) || (directoryExact && !trimmed))
			return;
		if (!directoryExact && completedSearches.has(trimmed)) return;
		const controller = new AbortController();
		searchController = controller;
		searching = true;
		try {
			const signal = AbortSignal.any([controller.signal, lifetime.signal]);
			const found = (
				await Promise.all(
					(['user', 'group'] as const).map((kind) =>
						searchDirectoryPrincipals(trimmed, kind, { hostname, signal })
					)
				)
			).flat();
			if (controller.signal.aborted) return;
			const sids = new Set(found.map((principal) => principal.sid));
			directoryResults = [
				...directoryResults.filter((principal) => !sids.has(principal.sid)),
				...found
			];
			if (!directoryExact) completedSearches.add(trimmed);
		} catch (error) {
			if (controller.signal.aborted || lifetime.signal.aborted) return;
			searchError =
				error instanceof Error && error.message
					? `The directory couldn’t be searched: ${error.message}`
					: 'The directory couldn’t be searched';
		} finally {
			if (searchController === controller) {
				searchController = undefined;
				searching = false;
			}
		}
	}

	watch(
		() => debouncedQuery.current,
		(query) => void searchDirectory(query),
		{ lazy: true }
	);

	void loadDatasets();
	void loadPrincipals();
	void loadConfig();
	void loadDirectory();
	onDestroy(() => {
		lifetime.abort();
		searchController?.abort();
	});

	function reset() {
		if (locked) return;
		draft = structuredClone(initial);
		applyExisting = false;
		touched = {};
		attempted = false;
		saveError = '';
	}

	function input(): SambaShareInput {
		const guest = draft.mode !== 'signed-in';
		const ids = (keys: string[], prefix: string) =>
			keys.filter((key) => key.startsWith(prefix)).map((key) => key.slice(prefix.length));
		const set = (keys: string[]) =>
			guest
				? { userIds: [], groupIds: [], directorySids: [] }
				: {
						userIds: ids(keys, 'u:').map(Number),
						groupIds: ids(keys, 'g:').map(Number),
						directorySids: ids(keys, 'ad:')
					};
		return {
			name: draft.name.trim(),
			dataset: draft.dataset,
			enabled: draft.enabled,
			permissions: { read: set(draft.read), write: set(draft.write) },
			guest: { enabled: guest, writeable: draft.mode === 'guest-write' },
			createMask: draft.createMask.trim(),
			directoryMask: draft.directoryMask.trim(),
			timeMachine: draft.timeMachine,
			timeMachineMaxSize: draft.timeMachine ? Number(draft.timeMachineMaxSize.trim()) : 0,
			auditEnabled: draft.auditEnabled,
			auditRetentionDays: draft.auditEnabled
				? Number(draft.auditRetentionDays.trim())
				: (target?.auditRetentionDays ?? 70),
			auditedOperations: draft.auditEnabled ? draft.auditedOperations : [],
			extraShareConfig: draft.extraShareConfig,
			applyToExisting: draft.enabled && applyExisting
		};
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		if (readOnly) return;
		attempted = true;
		if (saving || !valid || !canSave) {
			if (!tabHasError(tab)) {
				const next = TABS.find((entry) => tabHasError(entry.value));
				if (next) tab = next.value;
			}
			return;
		}
		saving = true;
		saveError = '';
		const name = draft.name.trim();
		try {
			const response = await saveSambaShare(input(), target?.id, {
				hostname,
				signal: lifetime.signal
			});
			if (lifetime.signal.aborted) return;
			if (response.status === 'success' || response.message === 'samba_apply_failed') {
				if (response.status === 'success') {
					toast.success(target ? `Share ${name} saved` : `Share ${name} created`, {
						position: 'bottom-center'
					});
				} else {
					toast.warning(shareSaveError(response), { position: 'bottom-center' });
				}
				void onSaved();
				onClose();
				return;
			}
			saveError = shareSaveError(response);
			if (target) void onSaved();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = 'The share couldn’t be saved.';
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

{#snippet hint(label: string, description: string)}
	{@const helpId = `${id}-${label.toLowerCase().replaceAll(' ', '-')}-help`}
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

{#snippet textField(
	key: 'name' | 'createMask' | 'directoryMask' | 'timeMachineMaxSize' | 'auditRetentionDays',
	text: string,
	placeholder: string,
	description?: string,
	inputmode?: 'numeric',
	wide = false
)}
	{@const error = problem(key)}
	<div class="min-w-0 space-y-2 {wide ? 'sm:col-span-2' : ''}">
		{@render label(key, text, description)}
		<Input
			id="{id}-{key}"
			bind:value={draft[key]}
			{placeholder}
			{inputmode}
			autocomplete="off"
			spellcheck={false}
			disabled={locked}
			class={key === 'createMask' || key === 'directoryMask' ? 'font-mono text-sm' : undefined}
			onblur={() => (touched[key] = true)}
			aria-invalid={error ? true : undefined}
			aria-describedby={error ? `${id}-${key}-error` : undefined}
		/>
	</div>
{/snippet}

{#snippet checkRow(
	key: string,
	text: string,
	checked: boolean,
	onChange: (checked: boolean) => void,
	description: string,
	blocked = ''
)}
	<div class="flex items-center justify-between gap-2 sm:col-span-2">
		{#if blocked}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					id="{id}-{key}"
					role="checkbox"
					aria-checked={checked}
					aria-disabled="true"
					class="focus-visible:ring-ring flex cursor-help items-center gap-2 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
				>
					<span
						class="border-input flex size-4 shrink-0 items-center justify-center rounded-lg border opacity-50 shadow-xs {checked
							? 'bg-primary text-primary-foreground border-primary'
							: 'dark:bg-input/30'}"
						aria-hidden="true"
					>
						{#if checked}<span class="icon-[lucide--check] size-3.5"></span>{/if}
					</span>
					<span class="text-muted-foreground text-sm font-medium">{text}</span>
					<span class="sr-only">: {blocked}</span>
				</Tooltip.Trigger>
				<Tooltip.Content side="top" align="start" sideOffset={6} class="z-60 max-w-64 text-left">
					{blocked}
				</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			<div class="flex items-center gap-2">
				<Checkbox
					id="{id}-{key}"
					{checked}
					disabled={locked}
					onCheckedChange={(value) => onChange(value === true)}
				/>
				<Label for="{id}-{key}">{text}</Label>
			</div>
		{/if}
		{@render hint(text, description)}
	</div>
{/snippet}

{#snippet datasetPicker()}
	{@const error = problem('dataset')}
	<div class="min-w-0 space-y-2">
		{@render label(
			'dataset',
			'Dataset',
			'The ZFS file system to share. The root file system, system folders, boot environments and Sylve’s own data can’t be shared.'
		)}
		<DatasetPicker
			id="{id}-dataset"
			value={draft.dataset}
			label={selectedDataset?.name || (draft.dataset && target?.datasetName) || ''}
			placeholder={draft.dataset && datasets
				? 'Missing dataset'
				: datasets === null && !datasetsFailed
					? 'Loading datasets…'
					: 'Choose a dataset'}
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

{#snippet accessSection()}
	{@const error = problem('access')}
	<section class="space-y-3" aria-labelledby="{id}-access-heading">
		<div class="flex min-h-5 items-center justify-between gap-2">
			<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
				<h3 id="{id}-access-heading" class="text-sm font-medium">Who Can Connect</h3>
				{#if error}
					<span id="{id}-access-error" class="text-destructive text-xs">({error})</span>
				{/if}
			</div>
			{@render hint(
				'Who Can Connect',
				'Signed-in users connect with their system user name and Samba password; set the password when editing the user. Guests connect without signing in.'
			)}
		</div>
		<RadioGroup.Root
			bind:value={draft.mode}
			disabled={locked}
			class="grid gap-2 sm:grid-cols-3"
			aria-labelledby="{id}-access-heading"
		>
			{#each ACCESS_MODES as mode (mode.value)}
				<label
					for="{id}-mode-{mode.value}"
					class="group has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-muted/40 flex min-w-0 cursor-pointer flex-col gap-2 rounded-lg border p-3 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-70"
				>
					<span class="flex items-center justify-between gap-2">
						<span
							class="bg-muted text-muted-foreground group-has-[[data-state=checked]]:bg-zen-highlight group-has-[[data-state=checked]]:text-zen-highlight-foreground flex size-8 items-center justify-center rounded-md"
							aria-hidden="true"
						>
							<span class="{mode.icon} size-4"></span>
						</span>
						<RadioGroup.Item value={mode.value} id="{id}-mode-{mode.value}" />
					</span>
					<span class="min-w-0 space-y-1">
						<span class="block text-sm leading-tight font-medium">{mode.label}</span>
						<span class="text-muted-foreground block text-xs leading-snug">{mode.description}</span>
					</span>
				</label>
			{/each}
		</RadioGroup.Root>
		{#if draft.mode === 'signed-in'}
			<div class="grid gap-4 sm:grid-cols-2">
				<PrincipalPicker
					id="{id}-read"
					label="Can Read"
					placeholder={principals || principalsFailed ? 'Choose users or groups' : 'Loading…'}
					{options}
					value={draft.read}
					notes={readNotes}
					disabled={locked}
					invalid={Boolean(error)}
					describedBy={error ? `${id}-access-error` : undefined}
					searching={directoryEnabled && searching}
					searchError={directoryEnabled ? searchError : ''}
					onToggle={(key) => toggle('read', key)}
					onSearch={directoryEnabled ? (query) => (searchQuery = query) : undefined}
				/>
				<PrincipalPicker
					id="{id}-write"
					label="Can Read & Write"
					placeholder={principals || principalsFailed ? 'Choose users or groups' : 'Loading…'}
					{options}
					value={draft.write}
					notes={writeNotes}
					disabled={locked}
					invalid={Boolean(error)}
					describedBy={error ? `${id}-access-error` : undefined}
					searching={directoryEnabled && searching}
					searchError={directoryEnabled ? searchError : ''}
					onToggle={(key) => toggle('write', key)}
					onSearch={directoryEnabled ? (query) => (searchQuery = query) : undefined}
				/>
			</div>
			{#if principalsFailed}
				<div class="flex flex-wrap items-center gap-3 text-sm">
					<span
						class="icon-[lucide--triangle-alert] size-4 text-amber-600 dark:text-amber-400"
						aria-hidden="true"
					></span>
					<p class="min-w-0 flex-1">Users and groups couldn’t be loaded.</p>
					<Button variant="outline" size="sm" onclick={() => void loadPrincipals()}
						>Try Again</Button
					>
				</div>
			{/if}
		{/if}
		{#if draft.enabled && !readOnly}
			{@render checkRow(
				'apply-existing',
				'Also apply to existing files and folders',
				applyExisting,
				(checked) => (applyExisting = checked),
				'Access is set on the dataset’s top folder, and new files and folders inherit it. Turn this on to also update everything already inside, in the background. Other datasets inside aren’t changed, and turning guest access off only changes the top folder.'
			)}
		{/if}
	</section>
{/snippet}

{#snippet scroller(label: string, content: Snippet)}
	<ScrollArea
		class="h-full"
		fade
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'overscroll-contain',
			role: 'region',
			'aria-label': label,
			tabindex: 0
		}}
	>
		<div class="px-4 pb-4">{@render content()}</div>
	</ScrollArea>
{/snippet}

{#snippet general()}
	<div class="grid gap-4 sm:grid-cols-2">
		{@render textField(
			'name',
			'Name',
			'Documents',
			`The name clients see when they browse this node. Up to ${MAX_SHARE_NAME_LENGTH} characters.`
		)}
		{@render datasetPicker()}
		{@render checkRow(
			'enabled',
			'Enabled',
			draft.enabled,
			(checked) => (draft.enabled = checked),
			'Disabled shares keep their settings but aren’t available to clients.'
		)}
		{@render checkRow(
			'timeMachine',
			'Time Machine',
			draft.timeMachine,
			(checked) => (draft.timeMachine = checked),
			'Lets Macs use this share for Time Machine backups. Needs Apple extensions in Samba settings.',
			readOnly || draft.timeMachine ? '' : timeMachineBlock
		)}
		{#if draft.timeMachine}
			{@render textField(
				'timeMachineMaxSize',
				'Backup Size Limit (GiB)',
				'0',
				'How much space Time Machine backups can use. 0 means no limit.',
				'numeric'
			)}
		{/if}
	</div>
{/snippet}

{#snippet audit()}
	<div class="grid gap-4 sm:grid-cols-2">
		{@render checkRow(
			'auditEnabled',
			'Audit Logging',
			draft.auditEnabled,
			(checked) => {
				draft.auditEnabled = checked;
				if (checked && draft.auditedOperations.length === 0)
					draft.auditedOperations = [...DEFAULT_AUDIT];
			},
			'Records the chosen operations in the Samba audit log.'
		)}
		{#if draft.auditEnabled}
			{@const error = problem('auditedOperations')}
			<fieldset class="min-w-0 space-y-2 sm:col-span-2">
				<legend class="flex flex-wrap items-baseline gap-x-1 text-sm font-medium">
					Operations
					{#if error}<span class="text-destructive text-xs font-normal">({error})</span>{/if}
				</legend>
				<div class="divide-y rounded-lg border">
					{#each AUDIT_GROUPS as group (group.key)}
						{@const chosen = group.operations.filter((value) =>
							draft.auditedOperations.includes(value)
						)}
						<div class="grid gap-3 p-3 sm:grid-cols-[17rem_1fr] sm:items-center">
							<div class="flex items-start gap-2">
								<Checkbox
									id="{id}-audit-group-{group.key}"
									class="mt-0.5"
									checked={chosen.length === group.operations.length}
									indeterminate={chosen.length > 0 && chosen.length < group.operations.length}
									disabled={locked}
									onCheckedChange={(checked) => toggleGroup(group.operations, checked === true)}
								/>
								<Label for="{id}-audit-group-{group.key}" class="block space-y-0.5">
									<span class="block">{group.label}</span>
									<span class="text-muted-foreground block text-xs font-normal">
										{group.description}
									</span>
								</Label>
							</div>
							<div class="flex flex-wrap gap-x-5 gap-y-2 pl-6 sm:pl-0">
								{#each group.operations as value (value)}
									<div class="flex items-center gap-2">
										<Checkbox
											id="{id}-audit-{value}"
											checked={draft.auditedOperations.includes(value)}
											disabled={locked}
											onCheckedChange={(checked) => toggleOperation(value, checked === true)}
										/>
										<Label for="{id}-audit-{value}" class="font-normal whitespace-nowrap"
											>{auditOperationLabel(value)}</Label
										>
									</div>
								{/each}
							</div>
						</div>
					{/each}
				</div>
			</fieldset>
			{@render textField(
				'auditRetentionDays',
				'Keep Entries For (Days)',
				'70',
				'How long audit log entries for this share are kept. 0 keeps them forever.',
				'numeric',
				true
			)}
		{/if}
	</div>
{/snippet}

{#snippet advancedSettings()}
	<div class="grid gap-4 sm:grid-cols-2">
		{@render textField(
			'createMask',
			'Create Mask',
			'0664',
			'The most permissive mode a new file can get, in octal. 0664 lets the owner and group write.'
		)}
		{@render textField(
			'directoryMask',
			'Directory Mask',
			'2775',
			'The most permissive mode a new folder can get, in octal. 2775 lets the owner and group write, and new files keep the folder’s group.'
		)}
		<div class="min-w-0 space-y-2 sm:col-span-2">
			{@render label(
				'extraShareConfig',
				'Extra Configuration',
				'Lines added to the end of this share’s section in smb4.conf as written, so they override the settings in the other tabs. Samba checks them before saving. They run with root privileges.'
			)}
			<Textarea
				id="{id}-extraShareConfig"
				bind:value={draft.extraShareConfig}
				placeholder="Veto files = /.DS_Store/"
				rows={6}
				spellcheck={false}
				disabled={locked}
				class="font-mono text-sm"
				onblur={() => (touched.extraShareConfig = true)}
				aria-invalid={problem('extraShareConfig') ? true : undefined}
				aria-describedby={problem('extraShareConfig') ? `${id}-extraShareConfig-error` : undefined}
			/>
		</div>
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
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save} novalidate>
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
							{target ? (readOnly ? target.name : `Edit Share — ${target.name}`) : 'New Share'}
						</span>
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						{readOnly
							? `The settings of the Samba share ${target?.name ?? ''}.`
							: target
								? `Change the settings of the Samba share ${target.name}.`
								: 'Share a ZFS file system over SMB.'}
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
					{@render scroller('Access', accessSection)}
				</Tabs.Content>
				<Tabs.Content value="audit" class="mt-0 min-h-0" tabindex={-1}>
					{@render scroller('Audit', audit)}
				</Tabs.Content>
				<Tabs.Content value="advanced" class="mt-0 min-h-0" tabindex={-1}>
					{@render scroller('Advanced', advancedSettings)}
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
						disabled={saving || (Boolean(target) && !canSave)}
						onclick={reset}
					>
						Reset
					</Button>
					<Button
						type="submit"
						class="h-9 gap-2"
						disabled={saving || (Boolean(target) && !canSave) || (attempted && !valid)}
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
