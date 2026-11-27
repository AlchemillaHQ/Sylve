<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getActiveDirectory } from '$lib/api/directory-services';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import { getSambaConfigResult, updateSambaConfig } from '$lib/api/samba/config';
	import { getSambaStatus } from '$lib/api/samba/share';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { ActiveDirectory } from '$lib/types/directory-services';
	import type { Iface } from '$lib/types/network/iface';
	import { isSwitchList, type SwitchList } from '$lib/types/network/switch';
	import type { SambaConfig } from '$lib/types/samba/config';
	import type { SambaStatus } from '$lib/types/samba/shares';
	import { SambaConfigSchema } from '$lib/types/samba/config';
	import {
		APIRequestError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { buildHostInterfaceOptions } from '$lib/utils/network/helpers';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import Connections from './Connections.svelte';
	import InterfacePicker from './InterfacePicker.svelte';
	import {
		cachedStatus,
		charsetProblem,
		configSaveError,
		globalConfigProblem,
		plural,
		serverStringProblem,
		workgroupProblem
	} from './samba';
	import StatusStrip from './StatusStrip.svelte';

	let {
		data
	}: {
		data: {
			hostname: string;
			config: SambaConfig | APIResponse;
			fresh: boolean;
			status: SambaStatus | APIResponse | null;
			statusFresh: boolean;
		};
	} = $props();

	type Draft = {
		unixCharset: string;
		workgroup: string;
		serverString: string;
		interfaces: string[];
		bindInterfacesOnly: boolean;
		appleExtensions: boolean;
		advertiseMdns: boolean;
		extraGlobalConfig: string;
	};

	const id = $props.id();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const hostname = initial.hostname;
	const lifetime = new AbortController();

	function toDraft(config: SambaConfig): Draft {
		return {
			unixCharset: config.unixCharset,
			workgroup: config.workgroup,
			serverString: config.serverString,
			interfaces: config.interfaces
				.split(',')
				.map((name) => name.trim())
				.filter(Boolean),
			bindInterfacesOnly: config.bindInterfacesOnly,
			appleExtensions: config.appleExtensions,
			advertiseMdns: config.advertiseMdns,
			extraGlobalConfig: config.extraGlobalConfig
		};
	}

	function notInitialized(result: unknown): boolean {
		return isAPIResponse(result) && result.message === 'samba_not_initialized';
	}

	const initiallyMissing = notInitialized(initial.config);
	const initialDraft = isAPIResponse(initial.config) ? null : toDraft(initial.config);
	let missing = $state(initiallyMissing);
	let initialFailure = $state(isAPIResponse(initial.config) && !initiallyMissing);
	const config = resource(
		() => hostname,
		async (host, _previous, { signal }): Promise<SambaConfig | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getSambaConfigResult({ hostname: host, signal: requestSignal });
			requestSignal.throwIfAborted();
			missing = notInitialized(result);
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('samba-config', result, host);
			return result;
		},
		{
			initialValue: isAPIResponse(initial.config) ? undefined : initial.config,
			lazy: initial.fresh
		}
	);
	const status = resource(
		() => hostname,
		async (host, _previous, { signal }): Promise<SambaStatus | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getSambaStatus({ hostname: host, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			void updateCache('samba-status', result, host);
			return result;
		},
		{
			initialValue: cachedStatus(initial.status),
			lazy: initial.statusFresh && Boolean(cachedStatus(initial.status))
		}
	);

	let saved = $state<Draft | null>(initialDraft);
	let draft = $state<Draft | null>(initialDraft ? structuredClone(initialDraft) : null);
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let advanced = $state(Boolean(initialDraft?.extraGlobalConfig));
	let confirmRestart = $state(false);
	let connectionsOpen = $state(false);
	let canModify = $derived(allowed('Sys.Modify', nodePath(hostname)));
	let locked = $derived(saving || !canModify);
	let failed = $derived(initialFailure || (Boolean(config.error) && !missing));
	let dirty = $derived(
		draft !== null &&
			saved !== null &&
			JSON.stringify(normalized(draft)) !== JSON.stringify(normalized(saved))
	);
	let clients = $derived(status.current?.running ? status.current.sessions.length : 0);

	let interfaces = $state<Iface[] | null>(null);
	let switches = $state<SwitchList | undefined>(undefined);
	let interfacesFailed = $state(false);
	let directory = $state<ActiveDirectory | null>(null);
	let interfaceOptions = $derived(
		interfaces
			? buildHostInterfaceOptions({
					interfaces: interfaces.filter((iface) => !iface.groups?.includes('tap')),
					switches
				})
			: []
	);
	let directoryWorkgroup = $derived(
		directory?.enabled && directory.domain ? directory.workgroup || directory.domain : ''
	);
	let directoryManaged = $derived(
		Boolean(directory && (directory.enabled || directory.joined || directory.membershipPending))
	);

	let problems = $derived<Record<string, string>>(
		draft
			? {
					unixCharset: charsetProblem(draft.unixCharset),
					workgroup: workgroupProblem(draft.workgroup),
					serverString: serverStringProblem(draft.serverString),
					interfaces: interfaceProblem(draft.interfaces),
					extraGlobalConfig: globalConfigProblem(draft.extraGlobalConfig)
				}
			: {}
	);
	let valid = $derived(Object.values(problems).every((problem) => !problem));

	function normalized(value: Draft): Draft {
		return {
			...value,
			unixCharset: value.unixCharset.trim(),
			workgroup: value.workgroup.trim(),
			serverString: value.serverString.trim()
		};
	}

	function interfaceProblem(names: string[]): string {
		if (!interfaces) return '';
		for (const name of names) {
			const option = interfaceOptions.find((entry) => entry.value === name);
			if (!option) return `${name} no longer exists`;
			if (option.disabled) return `Use the host VLAN interface instead of ${name}`;
		}
		return '';
	}

	function problem(key: keyof Draft): string {
		const value = problems[key] ?? '';
		if (attempted || key === 'interfaces') return value;
		const current = draft?.[key];
		return touched[key] && typeof current === 'string' && current.trim() ? value : '';
	}

	watch(
		() => config.current,
		(current) => {
			if (!current) return;
			const next = toDraft(current);
			if (!draft || !dirty) {
				saved = next;
				draft = structuredClone(next);
				advanced ||= Boolean(next.extraGlobalConfig);
			} else {
				saved = next;
			}
		},
		{ lazy: true }
	);

	async function loadNetwork() {
		interfacesFailed = false;
		const [ifaceResult, switchResult] = await Promise.all([
			getInterfaces({ hostname, signal: lifetime.signal }).catch(() => null),
			getSwitches(hostname).catch(() => null)
		]);
		if (lifetime.signal.aborted) return;
		if (!ifaceResult || isAPIResponse(ifaceResult)) {
			interfacesFailed = true;
			return;
		}
		interfaces = ifaceResult;
		switches = isSwitchList(switchResult) ? switchResult : undefined;
	}

	async function loadDirectory() {
		try {
			const result = await getActiveDirectory({ hostname, signal: lifetime.signal });
			if (!lifetime.signal.aborted) directory = result;
		} catch {
			directory = null;
		}
	}

	function refresh() {
		if (!config.loading) void config.refetch();
		if (!status.loading) void status.refetch();
	}

	void loadNetwork();
	void loadDirectory();
	const interval = useInterval(() => (connectionsOpen ? 5_000 : storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (storage.visible && !status.loading) void status.refetch();
		}
	});
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	function toggleInterface(name: string) {
		if (!draft) return;
		draft.interfaces = draft.interfaces.includes(name)
			? draft.interfaces.filter((entry) => entry !== name)
			: [...draft.interfaces, name];
	}

	function reset() {
		if (locked || !saved) return;
		draft = structuredClone($state.snapshot(saved));
		touched = {};
		attempted = false;
		saveError = '';
	}

	function submit(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (locked || !draft || !valid || !dirty) {
			if (problems.extraGlobalConfig) advanced = true;
			return;
		}
		if (clients > 0) confirmRestart = true;
		else void save();
	}

	async function save() {
		if (!draft) return;
		confirmRestart = false;
		saving = true;
		saveError = '';
		const value = normalized($state.snapshot(draft));
		try {
			const response = await updateSambaConfig(
				{
					unixCharset: value.unixCharset,
					workgroup: value.workgroup,
					serverString: value.serverString,
					interfaces: value.interfaces.join(','),
					bindInterfacesOnly: value.bindInterfacesOnly,
					appleExtensions: value.appleExtensions,
					advertiseMdns: value.advertiseMdns,
					extraGlobalConfig: value.extraGlobalConfig
				},
				{ hostname, signal: lifetime.signal }
			);
			if (lifetime.signal.aborted) return;
			if (response.status === 'success' || response.message === 'samba_apply_failed') {
				const parsed = SambaConfigSchema.safeParse(response.data);
				if (parsed.success) {
					saved = toDraft(parsed.data);
					draft = structuredClone(toDraft(parsed.data));
					config.mutate(parsed.data);
					void updateCache('samba-config', parsed.data, hostname);
				} else {
					saved = structuredClone(value);
					draft = structuredClone(value);
					void config.refetch();
				}
				touched = {};
				attempted = false;
				if (response.status === 'success') {
					toast.success('Samba settings saved', { position: 'bottom-center' });
				} else {
					toast.warning(configSaveError(response), { position: 'bottom-center' });
				}
				void status.refetch();
				return;
			}
			saveError = configSaveError(response);
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = 'The settings couldn’t be saved.';
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
		<Tooltip.Content side="top" align="end" sideOffset={6} class="max-w-64 text-left leading-5">
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet label(key: keyof Draft, text: string, description: string)}
	{@const error = problem(key)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<Label for="{id}-{key}">{text}</Label>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{@render hint(text, description)}
	</div>
{/snippet}

{#snippet textField(
	key: 'unixCharset' | 'workgroup' | 'serverString',
	text: string,
	placeholder: string,
	description: string,
	fixedValue = ''
)}
	{@const error = problem(key)}
	{@const helpId = `${id}-${text.toLowerCase().replaceAll(' ', '-')}-help`}
	<div class="min-w-0 space-y-2">
		{@render label(key, text, description)}
		{#if fixedValue}
			<Input id="{id}-{key}" value={fixedValue} disabled aria-describedby={helpId} />
		{:else if draft}
			<Input
				id="{id}-{key}"
				bind:value={draft[key]}
				{placeholder}
				autocomplete="off"
				spellcheck={false}
				disabled={locked}
				onblur={() => (touched[key] = true)}
				aria-invalid={error ? true : undefined}
				aria-describedby={error ? `${id}-${key}-error` : undefined}
			/>
		{/if}
	</div>
{/snippet}

{#snippet checkRow(
	key: 'bindInterfacesOnly' | 'appleExtensions' | 'advertiseMdns',
	text: string,
	description: string,
	className = ''
)}
	{#if draft}
		<div class="min-w-0 {className}">
			<div
				class="border-input flex h-9 min-w-0 items-center justify-between gap-2 rounded-md border px-3 shadow-xs dark:bg-input/30"
			>
				<div class="flex min-w-0 items-center gap-2">
					<Checkbox id="{id}-{key}" bind:checked={draft[key]} disabled={locked} />
					<Label for="{id}-{key}" class="truncate">{text}</Label>
				</div>
				{@render hint(text, description)}
			</div>
		</div>
	{/if}
{/snippet}

{#snippet section(title: string, body: Snippet, columns: string)}
	{@const headingId = `${id}-${title.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`}
	<section class="bg-card rounded-lg border" aria-labelledby={headingId}>
		<h2 id={headingId} class="border-b px-4 py-3 text-sm font-semibold">
			{title}
		</h2>
		<div class="grid gap-4 p-4 {columns}">{@render body()}</div>
	</section>
{/snippet}

{#snippet serverFields()}
	{@render textField(
		'workgroup',
		'Workgroup',
		'WORKGROUP',
		directoryWorkgroup
			? `Active Directory sets the workgroup to ${directoryWorkgroup} while this node is a member of ${directory?.domain}, so it can’t be changed here.`
			: 'The Windows workgroup clients see this node in. Up to 15 letters, digits, hyphens or underscores.',
		directoryWorkgroup
	)}
	{@render textField(
		'serverString',
		'Description',
		'Sylve SMB Server',
		'The description clients show next to this server. One line, up to 100 bytes.'
	)}
	{@render textField(
		'unixCharset',
		'Character Set',
		'UTF-8',
		'The character set file names are stored in on this node. Keep UTF-8 unless your files use another.'
	)}
{/snippet}

{#snippet networkFields()}
	{#if draft}
		<div class="min-w-0 space-y-2 @xl:col-span-2">
			{@render label(
				'interfaces',
				'Interfaces',
				'The network interfaces Samba listens on. If none are chosen, Samba only listens on lo0, so it’s reachable from this node only.'
			)}
			<InterfacePicker
				id="{id}-interfaces"
				options={interfaceOptions}
				value={draft.interfaces}
				loading={interfaces === null && !interfacesFailed}
				failed={interfacesFailed}
				disabled={locked}
				error={problem('interfaces')}
				onRetry={() => void loadNetwork()}
				onToggle={toggleInterface}
			/>
		</div>
	{/if}
	{@render checkRow(
		'bindInterfacesOnly',
		'Listen Only on These Interfaces',
		'When off, Samba also accepts connections on every other interface of this node.',
		'@xl:pt-7'
	)}
{/snippet}

{#snippet appleFields()}
	{@render checkRow(
		'appleExtensions',
		'Apple Extensions',
		'Improves how Macs handle files and allows Time Machine shares. Clients must use SMB2 or newer.'
	)}
	{@render checkRow(
		'advertiseMdns',
		'Advertise on the Network',
		'Lets Macs and other devices find this server automatically over mDNS. Turning this on also turns on the mDNS service.'
	)}
{/snippet}

<svelte:head><title>Samba Settings · {hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[material-symbols--smb-share] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Samba</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Settings</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<Button
			variant="outline"
			size="icon"
			class="size-9"
			disabled={config.loading}
			onclick={refresh}
			aria-label="Refresh settings"
		>
			<span
				class="icon-[lucide--refresh-cw] size-4"
				class:animate-spin={config.loading}
				aria-hidden="true"
			></span>
		</Button>
	</header>
	<StatusStrip
		status={status.current}
		failed={Boolean(status.error)}
		loading={status.loading}
		subject="clients can’t connect"
		onConnections={() => (connectionsOpen = true)}
	/>
	{#if missing}
		<div
			class="text-muted-foreground flex flex-col items-center justify-center gap-2 rounded-lg border p-8 text-center"
		>
			<span class="icon-[material-symbols--smb-share] size-8" aria-hidden="true"></span>
			<p class="text-foreground font-medium">Samba Isn’t Set Up Yet</p>
			<p class="text-sm">Turn on the Samba service in Services to configure it.</p>
		</div>
	{:else if failed}
		<div
			class="bg-card mb-4 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span
				class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
				aria-hidden="true"
			></span>
			<p class="min-w-0 flex-1">
				{draft
					? 'Settings could not be refreshed. Showing the last saved values.'
					: 'Settings could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={config.loading} onclick={refresh}>
				Try Again
			</Button>
		</div>
	{/if}
	{#if draft && !missing}
		<form class="space-y-4" onsubmit={submit} novalidate>
			{@render section('Server', serverFields, '@xl:grid-cols-3')}
			{@render section('Network', networkFields, '@xl:grid-cols-3')}
			{@render section('Apple & Discovery', appleFields, '@xl:grid-cols-2')}
			<section class="bg-card rounded-lg border">
				<button
					type="button"
					class="hover:bg-muted/40 focus-visible:ring-ring flex w-full cursor-pointer items-center gap-2 rounded-lg px-4 py-3 text-left text-sm font-semibold focus-visible:ring-2 focus-visible:outline-none"
					aria-expanded={advanced}
					aria-controls="{id}-advanced"
					onclick={() => (advanced = !advanced)}
				>
					<span
						class="{advanced
							? 'icon-[lucide--chevron-down]'
							: 'icon-[lucide--chevron-right]'} text-muted-foreground size-4 shrink-0"
						aria-hidden="true"
					></span>
					Advanced Settings
					{#if !advanced && draft.extraGlobalConfig !== saved?.extraGlobalConfig}
						<span class="text-muted-foreground text-xs font-normal">· Changed</span>
					{/if}
				</button>
				{#if advanced}
					<div id="{id}-advanced" class="space-y-2 border-t p-4">
						{@render label(
							'extraGlobalConfig',
							'Extra Configuration',
							directoryManaged
								? 'Lines added to the end of Samba’s [global] section as written, so they can override the settings above. While Active Directory manages this node, identity settings like idmap can’t be set here. Samba checks the whole configuration before saving. They run with root privileges.'
								: 'Lines added to the end of Samba’s [global] section as written, so they can override the settings above. Samba checks the whole configuration before saving. They run with root privileges.'
						)}
						<Textarea
							id="{id}-extraGlobalConfig"
							bind:value={draft.extraGlobalConfig}
							placeholder="Vfs mkdir use tmp name = no"
							rows={5}
							spellcheck={false}
							disabled={locked}
							class="font-mono text-sm"
							onblur={() => (touched.extraGlobalConfig = true)}
							aria-invalid={problem('extraGlobalConfig') ? true : undefined}
							aria-describedby={problem('extraGlobalConfig')
								? `${id}-extraGlobalConfig-error`
								: undefined}
						/>
					</div>
				{/if}
			</section>
			{#if canModify}
				{@const pending = dirty || saving || Boolean(saveError)}
				<div
					class="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 {pending
						? 'bg-zen-highlight border-zen-highlight-foreground/40 sticky bottom-4 z-10 shadow-lg'
						: 'bg-card'}"
				>
					<div class="min-w-0 flex-1 text-sm">
						{#if saveError}
							<p class="text-destructive break-words" role="alert">{saveError}</p>
						{:else if pending}
							<p class="flex items-center gap-2">
								<span
									class="icon-[lucide--circle-dot] text-zen-highlight-foreground size-4 shrink-0"
									aria-hidden="true"
								></span>
								<span>
									<span class="font-medium">Unsaved changes.</span>
									Saving restarts Samba{clients > 0
										? `, which disconnects ${plural(clients, 'connected client')}`
										: ''}.
								</span>
							</p>
						{:else}
							<p class="text-muted-foreground">Saving restarts Samba.</p>
						{/if}
					</div>
					<div class="flex items-center gap-2">
						<Button
							type="button"
							variant="outline"
							class="dark:bg-background dark:hover:bg-muted h-9"
							disabled={saving || !dirty}
							onclick={reset}
						>
							Reset
						</Button>
						<Button
							type="submit"
							class="h-9 gap-2"
							disabled={saving || !dirty || (attempted && !valid)}
						>
							{#if saving}
								<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
								></span>
							{/if}
							{saving ? 'Saving…' : 'Save Changes'}
						</Button>
					</div>
				</div>
			{:else}
				<p class="text-muted-foreground text-sm">
					You can view these settings but not change them.
				</p>
			{/if}
		</form>
	{/if}
</div>

{#if connectionsOpen && status.current}
	<Connections status={status.current} onClose={() => (connectionsOpen = false)} />
{/if}

{#if confirmRestart}
	<AlertDialog.Root
		open
		onOpenChange={(open) => {
			if (!open) confirmRestart = false;
		}}
	>
		<AlertDialog.Content>
			<AlertDialog.Header class="text-left">
				<AlertDialog.Title>Restart Samba?</AlertDialog.Title>
				<AlertDialog.Description class="text-pretty">
					Saving restarts Samba. {clients === 1 ? '1 client is' : `${clients} clients are`} connected
					and will be disconnected. Most clients reconnect on their own.
				</AlertDialog.Description>
			</AlertDialog.Header>
			<AlertDialog.Footer>
				<AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
				<AlertDialog.Action onclick={() => void save()}>Save and Restart</AlertDialog.Action>
			</AlertDialog.Footer>
		</AlertDialog.Content>
	</AlertDialog.Root>
{/if}
