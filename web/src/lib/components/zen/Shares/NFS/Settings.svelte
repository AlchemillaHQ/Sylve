<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getKerberosKeytabs } from '$lib/api/directory-services';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNFSConfigResult, getNFSStatusResult, saveNFSConfig } from '$lib/api/nfs';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { NFSConfig, NFSStatus } from '$lib/types/nfs';
	import { cn } from '$lib/utils.js';
	import {
		APIRequestError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { cachedStatus } from '../shared';
	import AddressPicker from './AddressPicker.svelte';
	import ApplyDialog from './ApplyDialog.svelte';
	import Preview from './Preview.svelte';
	import StatusStrip from './StatusStrip.svelte';
	import {
		applyPending,
		bindAddressProblem,
		fieldError,
		normalizedAddress,
		pendingDetail,
		requestError,
		type AddressOption,
		type SettingsData
	} from './nfs';

	let { data }: { data: SettingsData } = $props();

	type Draft = {
		protocol: NFSConfig['protocol'];
		v4MaxVersion: NFSConfig['v4MaxVersion'];
		numericIds: boolean;
		udp: boolean;
		allowNonroot: boolean;
		bindAddresses: string[];
		mountdPort: string;
		minThreads: string;
		maxThreads: string;
		kerberosPrincipal: string;
	};
	type Field =
		| 'protocol'
		| 'v4MaxVersion'
		| 'numericIds'
		| 'udp'
		| 'bindAddresses'
		| 'mountdPort'
		| 'threads'
		| 'kerberosPrincipal';

	const PROTOCOLS: { value: NFSConfig['protocol']; label: string }[] = [
		{ value: 'v3v4', label: 'NFSv3 and NFSv4' },
		{ value: 'v4', label: 'NFSv4 Only' },
		{ value: 'v3', label: 'NFSv3 Only' }
	];
	const VERSIONS: NFSConfig['v4MaxVersion'][] = ['4.2', '4.1', '4.0'];
	const SERVER_FIELDS: Record<string, Field> = {
		protocol: 'protocol',
		v4MaxVersion: 'v4MaxVersion',
		numericIds: 'numericIds',
		udp: 'udp',
		bindAddresses: 'bindAddresses',
		minThreads: 'threads',
		maxThreads: 'threads',
		mountdPort: 'mountdPort',
		kerberosPrincipal: 'kerberosPrincipal'
	};
	const SETTINGS_MESSAGES: Record<string, string> = {
		kerberos_principal_required: 'Shares use Kerberos, so choose one',
		kerberos_requires_names: 'Shares use Kerberos, so turn this off'
	};

	const id = $props.id();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const hostname = initial.hostname;
	const lifetime = new AbortController();

	function toDraft(config: NFSConfig): Draft {
		return {
			protocol: config.protocol,
			v4MaxVersion: config.v4MaxVersion,
			numericIds: config.numericIds,
			udp: config.udp,
			allowNonroot: config.allowNonroot,
			bindAddresses: [...config.bindAddresses],
			mountdPort: config.mountdPort ? String(config.mountdPort) : '',
			minThreads: config.minThreads ? String(config.minThreads) : '',
			maxThreads: config.maxThreads ? String(config.maxThreads) : '',
			kerberosPrincipal: config.kerberosPrincipal
		};
	}

	const initialConfig =
		initial.config && !isAPIResponse(initial.config) ? initial.config : undefined;
	let initialFailure = $state(!initialConfig);
	const config = resource(
		() => hostname,
		async (host, _previous, { signal }): Promise<NFSConfig | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getNFSConfigResult({ hostname: host, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('nfs-config', result, host);
			return result;
		},
		{ initialValue: initialConfig, lazy: initial.fresh }
	);
	const status = resource(
		() => hostname,
		async (host, _previous, { signal }): Promise<NFSStatus | undefined> => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getNFSStatusResult({ hostname: host, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			void updateCache('nfs-status', result, host);
			return result;
		},
		{
			initialValue: cachedStatus(initial.status),
			lazy: initial.statusFresh && Boolean(cachedStatus(initial.status))
		}
	);

	const initialDraft = initialConfig ? toDraft(initialConfig) : null;
	let saved = $state<Draft | null>(initialDraft);
	let draft = $state<Draft | null>(initialDraft ? structuredClone(initialDraft) : null);
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let serverErrors = $state<Partial<Record<Field, { message: string; value: string }>>>({});
	let previewOpen = $state(false);
	let applyOpen = $state(false);
	let canModify = $derived(allowed('Sys.Modify', nodePath(hostname)));
	let locked = $derived(saving || !canModify);
	let failed = $derived(initialFailure || Boolean(config.error));
	let dirty = $derived(
		draft !== null &&
			saved !== null &&
			JSON.stringify(normalized(draft)) !== JSON.stringify(normalized(saved))
	);
	let restarts = $derived(Boolean(status.current?.enabled && status.current.runtimeAvailable));
	let applying = $derived(
		status.current?.applyStatus === 'applying' || status.current?.applyStatus === 'starting'
	);

	let addresses = $state<AddressOption[] | null>(null);
	let addressesFailed = $state(false);
	let principals = $state<string[] | null>(null);
	let principalsFailed = $state(false);
	let principalChoices = $derived.by(() => {
		const list = principals ?? [];
		const current = draft?.kerberosPrincipal ?? '';
		return current && !list.includes(current) ? [current, ...list] : list;
	});

	let problems = $derived<Partial<Record<Field, string>>>(
		draft
			? {
					bindAddresses: bindAddressesProblem(draft.bindAddresses),
					mountdPort: portProblem(draft.mountdPort),
					threads: threadsProblem(draft.minThreads, draft.maxThreads)
				}
			: {}
	);
	let valid = $derived(Object.values(problems).every((problem) => !problem));

	function normalized(value: Draft): Draft {
		const v4 = value.protocol !== 'v3';
		return {
			...value,
			udp: value.protocol === 'v4' ? false : value.udp,
			numericIds: v4 && value.numericIds,
			bindAddresses: [...value.bindAddresses].sort(),
			mountdPort: value.mountdPort.trim(),
			minThreads: value.minThreads.trim(),
			maxThreads: value.maxThreads.trim(),
			kerberosPrincipal: value.kerberosPrincipal.trim()
		};
	}

	function bindAddressesProblem(list: string[]): string {
		if (list.length > 32) return 'Choose at most 32 addresses';
		return list.some((address) => bindAddressProblem(address))
			? 'Remove the invalid addresses'
			: '';
	}

	function portProblem(value: string): string {
		const trimmed = value.trim();
		if (!trimmed) return '';
		const port = Number(trimmed);
		return /^\d+$/.test(trimmed) && port >= 1 && port <= 65535 ? '' : 'Use a port from 1 to 65535';
	}

	function threadCountValid(value: string): boolean {
		return /^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 256;
	}

	function threadsProblem(min: string, max: string): string {
		const low = min.trim();
		const high = max.trim();
		if ([low, high].some((value) => value && !threadCountValid(value))) {
			return 'Use 1 to 256 threads';
		}
		if (!low !== !high) return 'Set both, or leave both empty';
		return low && Number(low) > Number(high) ? 'The minimum can’t be above the maximum' : '';
	}

	function threadsShown(): boolean {
		if (!draft) return false;
		const low = draft.minThreads.trim();
		const high = draft.maxThreads.trim();
		if (low && high) return Boolean(touched.minThreads || touched.maxThreads);
		if (touched.minThreads && touched.maxThreads) return true;
		return Boolean(
			(low && touched.minThreads && !threadCountValid(low)) ||
			(high && touched.maxThreads && !threadCountValid(high))
		);
	}

	function fieldValue(key: Field): string {
		if (!draft) return '';
		if (key === 'threads') return `${draft.minThreads}:${draft.maxThreads}`;
		return JSON.stringify(draft[key]);
	}

	function problem(key: Field): string {
		const server = serverErrors[key];
		if (server && server.value === fieldValue(key)) return server.message;
		const value = problems[key] ?? '';
		if (!value || attempted || key === 'bindAddresses') return value;
		if (key === 'threads') return threadsShown() ? value : '';
		return touched[key] ? value : '';
	}

	watch(
		() => config.current,
		(current) => {
			if (!current) return;
			const next = toDraft(current);
			if (!draft || !dirty) {
				saved = next;
				draft = structuredClone(next);
			} else {
				saved = next;
			}
		},
		{ lazy: true }
	);

	async function loadAddresses() {
		addressesFailed = false;
		const result = await getInterfaces({ hostname, signal: lifetime.signal }).catch(() => null);
		if (lifetime.signal.aborted) return;
		if (!result || isAPIResponse(result)) {
			addressesFailed = true;
			return;
		}
		const options: Record<string, AddressOption> = {};
		for (const iface of result) {
			const name = iface.description.trim()
				? `${iface.description.trim()} (${iface.name})`
				: iface.name;
			for (const { ip } of [...(iface.ipv4 ?? []), ...(iface.ipv6 ?? [])]) {
				if (ip.includes('/') || bindAddressProblem(ip)) continue;
				const value = normalizedAddress(ip);
				options[value] ??= { value, label: name };
			}
		}
		addresses = Object.values(options).sort((a, b) => a.label.localeCompare(b.label));
	}

	async function loadPrincipals() {
		principalsFailed = false;
		try {
			const keytabs = await getKerberosKeytabs({ hostname, signal: lifetime.signal });
			if (lifetime.signal.aborted) return;
			principals = [...new Set(keytabs.flatMap((keytab) => keytab.principals))]
				.filter((principal) => principal.startsWith('nfs/'))
				.sort();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			principalsFailed = true;
			principals = [];
		}
	}

	function refresh() {
		if (!config.loading) void config.refetch();
		if (!status.loading) void status.refetch();
	}

	void loadAddresses();
	void loadPrincipals();
	const interval = useInterval(() => (applying ? 2_000 : storage.idle ? 60_000 : 15_000), {
		callback: () => {
			if (storage.visible && !status.loading) void status.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !status.loading) void status.refetch();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	function setProtocol(value: NFSConfig['protocol']) {
		if (!draft) return;
		draft.protocol = value;
		if (value === 'v4') draft.udp = false;
	}

	function reset() {
		if (locked || !saved) return;
		draft = structuredClone($state.snapshot(saved));
		touched = {};
		attempted = false;
		saveError = '';
		serverErrors = {};
	}

	function submit(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (locked || !draft || !valid || !dirty) return;
		void save();
	}

	async function save() {
		if (!draft) return;
		saving = true;
		saveError = '';
		const value = normalized($state.snapshot(draft));
		try {
			const response = await saveNFSConfig(
				{
					protocol: value.protocol,
					v4MaxVersion: value.v4MaxVersion,
					numericIds: value.numericIds,
					udp: value.udp,
					allowNonroot: value.allowNonroot,
					bindAddresses: value.bindAddresses,
					mountdPort: Number(value.mountdPort || 0),
					minThreads: Number(value.minThreads || 0),
					maxThreads: Number(value.maxThreads || 0),
					kerberosPrincipal: value.kerberosPrincipal
				},
				{ hostname, signal: lifetime.signal }
			);
			if (lifetime.signal.aborted) return;
			if (response.status === 'success') {
				saved = structuredClone(value);
				draft = structuredClone(value);
				touched = {};
				attempted = false;
				serverErrors = {};
				if (applyPending(response)) {
					toast.warning(pendingDetail(response), { position: 'bottom-center' });
				} else {
					toast.success('NFS settings saved', { position: 'bottom-center' });
				}
				refresh();
				return;
			}
			const detail = fieldError(response);
			const key = detail ? SERVER_FIELDS[detail.field] : undefined;
			if (detail && key) {
				serverErrors[key] = {
					message: SETTINGS_MESSAGES[detail.code] ?? detail.message,
					value: fieldValue(key)
				};
			} else saveError = requestError(response, 'The settings couldn’t be saved.');
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = 'The settings couldn’t be saved.';
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
		<Tooltip.Content side="top" align="end" sideOffset={6} class="max-w-64 text-left leading-5">
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet label(key: Field, target: string, text: string, description: string, showError = true)}
	{@const error = showError ? problem(key) : ''}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<Label for="{id}-{target}">{text}</Label>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{@render hint(text, description)}
	</div>
{/snippet}

{#snippet blockedTrigger(triggerId: string, text: string, reason: string)}
	<Tooltip.Root delayDuration={500}>
		<Tooltip.Trigger
			type="button"
			id={triggerId}
			aria-disabled="true"
			class={cn(
				buttonVariants({ variant: 'outline' }),
				'w-full cursor-help justify-between px-3 font-normal opacity-50 aria-disabled:pointer-events-auto'
			)}
		>
			<span class="truncate">{text}</span>
			<span class="sr-only">: {reason}</span>
			<span class="icon-[lucide--chevron-down] size-4 shrink-0 opacity-50" aria-hidden="true"
			></span>
		</Tooltip.Trigger>
		<Tooltip.Content side="top" align="start" sideOffset={6} class="max-w-64 text-left">
			{reason}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet checkRow(
	key: 'numericIds' | 'udp' | 'allowNonroot',
	text: string,
	description: string,
	blocked = ''
)}
	{#if draft}
		{@const error = key === 'allowNonroot' || blocked ? '' : problem(key)}
		<div class="min-w-0">
			<div
				class="border-input flex min-h-9 min-w-0 items-center justify-between gap-2 rounded-md border px-3 py-1.5 shadow-xs dark:bg-input/30 {error
					? 'border-destructive'
					: ''}"
			>
				{#if blocked}
					<Tooltip.Root delayDuration={500}>
						<Tooltip.Trigger
							type="button"
							id="{id}-{key}"
							role="checkbox"
							aria-checked={draft[key]}
							aria-disabled="true"
							class="focus-visible:ring-ring flex min-w-0 cursor-help items-center gap-2 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
						>
							<span
								class="border-input flex size-4 shrink-0 items-center justify-center rounded-lg border opacity-50 shadow-xs {draft[
									key
								]
									? 'bg-primary text-primary-foreground border-primary'
									: 'dark:bg-input/30'}"
								aria-hidden="true"
							>
								{#if draft[key]}<span class="icon-[lucide--check] size-3.5"></span>{/if}
							</span>
							<span class="text-muted-foreground truncate text-sm font-medium">{text}</span>
							<span class="sr-only">: {blocked}</span>
						</Tooltip.Trigger>
						<Tooltip.Content side="top" align="start" sideOffset={6} class="max-w-64 text-left">
							{blocked}
						</Tooltip.Content>
					</Tooltip.Root>
				{:else}
					<div class="flex min-w-0 items-center gap-2">
						<Checkbox
							id="{id}-{key}"
							bind:checked={draft[key]}
							disabled={locked}
							aria-invalid={error ? true : undefined}
							aria-describedby={error ? `${id}-${key}-error` : undefined}
						/>
						<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
							<Label for="{id}-{key}">{text}</Label>
							{#if error}
								<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
							{/if}
						</div>
					</div>
				{/if}
				{@render hint(text, description)}
			</div>
		</div>
	{/if}
{/snippet}

{#snippet numberField(
	key: 'mountdPort' | 'minThreads' | 'maxThreads',
	field: Field,
	text: string,
	description: string
)}
	{#if draft}
		{@const error = problem(field)}
		<div class="min-w-0 space-y-2">
			{@render label(field, key, text, description, key !== 'maxThreads')}
			<Input
				id="{id}-{key}"
				bind:value={draft[key]}
				placeholder="Automatic"
				inputmode="numeric"
				autocomplete="off"
				disabled={locked}
				onblur={() => (touched[key] = true)}
				aria-invalid={error ? true : undefined}
				aria-describedby={error ? `${id}-${field}-error` : undefined}
			/>
		</div>
	{/if}
{/snippet}

{#snippet section(title: string, body: Snippet, columns: string)}
	{@const headingId = `${id}-section-${title.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`}
	<section class="bg-card rounded-lg border" aria-labelledby={headingId}>
		<h2 id={headingId} class="border-b px-4 py-3 text-sm font-semibold">{title}</h2>
		<div class="grid gap-4 p-4 {columns}">{@render body()}</div>
	</section>
{/snippet}

{#snippet protocolFields()}
	{#if draft}
		{@const v3Only = draft.protocol === 'v3'}
		<div class="min-w-0 space-y-2">
			{@render label(
				'protocol',
				'protocol',
				'Protocol',
				'Which NFS versions clients can use. NFSv4 works through firewalls more easily; NFSv3 suits older clients.'
			)}
			<Select.Root
				type="single"
				value={draft.protocol}
				onValueChange={(value) => setProtocol(value as NFSConfig['protocol'])}
				disabled={locked}
			>
				<Select.Trigger
					id="{id}-protocol"
					class="bg-background w-full dark:hover:bg-input/30"
					aria-invalid={problem('protocol') ? true : undefined}
					aria-describedby={problem('protocol') ? `${id}-protocol-error` : undefined}
				>
					{PROTOCOLS.find((entry) => entry.value === draft?.protocol)?.label}
				</Select.Trigger>
				<Select.Content>
					{#each PROTOCOLS as entry (entry.value)}
						<Select.Item value={entry.value} label={entry.label} />
					{/each}
				</Select.Content>
			</Select.Root>
		</div>
		<div class="min-w-0 space-y-2">
			{@render label(
				'v4MaxVersion',
				'v4MaxVersion',
				'Highest NFSv4 Version',
				'The newest NFSv4 minor version clients may use. Lower it only for clients that misbehave with newer versions.'
			)}
			{#if v3Only}
				{@render blockedTrigger(
					`${id}-v4MaxVersion`,
					`NFSv${draft.v4MaxVersion}`,
					'Only used with NFSv4'
				)}
			{:else}
				<Select.Root
					type="single"
					value={draft.v4MaxVersion}
					onValueChange={(value) => {
						if (draft) draft.v4MaxVersion = value as NFSConfig['v4MaxVersion'];
					}}
					disabled={locked}
				>
					<Select.Trigger
						id="{id}-v4MaxVersion"
						class="bg-background w-full dark:hover:bg-input/30"
						aria-invalid={problem('v4MaxVersion') ? true : undefined}
						aria-describedby={problem('v4MaxVersion') ? `${id}-v4MaxVersion-error` : undefined}
					>
						NFSv{draft.v4MaxVersion}
					</Select.Trigger>
					<Select.Content>
						{#each VERSIONS as version (version)}
							<Select.Item value={version} label="NFSv{version}" />
						{/each}
					</Select.Content>
				</Select.Root>
			{/if}
		</div>
		{@render checkRow(
			'numericIds',
			'Numeric User IDs',
			'NFSv4 sends user and group IDs as numbers instead of names. Turn it off to use Kerberos.',
			v3Only ? 'Only used with NFSv4' : ''
		)}
		{@render checkRow(
			'udp',
			'Allow UDP',
			'Lets NFSv3 clients connect over UDP as well as TCP. Only very old clients need it.',
			draft.protocol === 'v4' ? 'Only used with NFSv3' : ''
		)}
	{/if}
{/snippet}

{#snippet networkFields()}
	{#if draft}
		{@const error = problem('bindAddresses')}
		<div class="min-w-0 space-y-2 @xl:col-span-2">
			{@render label(
				'bindAddresses',
				'bindAddresses',
				'Listen Addresses',
				'The addresses NFS listens on. With none chosen, NFS listens on every address of this node.'
			)}
			<AddressPicker
				id="{id}-bindAddresses"
				options={addresses ?? []}
				value={draft.bindAddresses}
				loading={addresses === null && !addressesFailed}
				failed={addressesFailed}
				disabled={locked}
				invalid={Boolean(error)}
				describedBy={error ? `${id}-bindAddresses-error` : undefined}
				onRetry={() => void loadAddresses()}
				onChange={(next) => {
					if (draft) draft.bindAddresses = next;
				}}
			/>
		</div>
		{@render numberField(
			'mountdPort',
			'mountdPort',
			'Mountd Port',
			'A fixed port for the NFSv3 mount service, so it can pass a firewall. Leave empty to pick one automatically.'
		)}
		<div class="min-w-0 @xl:pt-7">
			{@render checkRow(
				'allowNonroot',
				'Allow Unprivileged Ports',
				'Accepts requests from client ports above 1023. Some clients, like macOS, need it; it’s less strict about who can connect.'
			)}
		</div>
	{/if}
{/snippet}

{#snippet performanceFields()}
	{@render numberField(
		'minThreads',
		'threads',
		'Minimum Threads',
		'The fewest NFS server threads to keep running. Leave both empty to size them automatically.'
	)}
	{@render numberField(
		'maxThreads',
		'threads',
		'Maximum Threads',
		'The most NFS server threads to run, from 1 to 256. Leave both empty to size them automatically.'
	)}
{/snippet}

{#snippet kerberosFields()}
	{#if draft}
		<div class="min-w-0 space-y-2">
			{@render label(
				'kerberosPrincipal',
				'kerberosPrincipal',
				'Kerberos Principal',
				'The nfs/ service principal from a Directory Services keytab. Shares using Kerberos need it.'
			)}
			<Select.Root
				type="single"
				value={draft.kerberosPrincipal || 'none'}
				onValueChange={(value) => {
					if (draft) draft.kerberosPrincipal = value === 'none' ? '' : value;
				}}
				disabled={locked}
			>
				<Select.Trigger
					id="{id}-kerberosPrincipal"
					class="bg-background w-full dark:hover:bg-input/30"
					aria-invalid={problem('kerberosPrincipal') ? true : undefined}
					aria-describedby={problem('kerberosPrincipal')
						? `${id}-kerberosPrincipal-error`
						: undefined}
				>
					<span class="truncate {draft.kerberosPrincipal ? 'font-mono text-sm' : ''}">
						{draft.kerberosPrincipal || 'None'}
					</span>
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="none" label="None" />
					{#each principalChoices as principal (principal)}
						<Select.Item value={principal} label={principal} class="font-mono text-sm" />
					{/each}
				</Select.Content>
			</Select.Root>
			{#if principalsFailed}
				<p class="text-muted-foreground text-xs">
					Keytabs couldn’t be read from Directory Services.
				</p>
			{:else if principals && principals.length === 0}
				<p class="text-muted-foreground text-xs">
					No keytab has an nfs/ principal. Add one in Directory Services to use Kerberos.
				</p>
			{/if}
		</div>
	{/if}
{/snippet}

<svelte:head><title>NFS Settings · {hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[mdi--folder-network] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">NFS</span>
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
		<div class="flex items-center gap-2">
			<Button variant="outline" class="h-9 gap-2" onclick={() => (previewOpen = true)}>
				<span class="icon-[lucide--file-search] size-4" aria-hidden="true"></span>
				Preview Exports
			</Button>
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
		</div>
	</header>
	<StatusStrip
		status={status.current}
		failed={Boolean(status.error)}
		loading={status.loading}
		subject="nothing is exported"
		onApply={canModify ? () => (applyOpen = true) : undefined}
	/>
	{#if failed}
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
	{#if draft}
		<form class="space-y-4" onsubmit={submit} novalidate>
			{@render section('Protocol', protocolFields, '@xl:grid-cols-2')}
			{@render section('Network', networkFields, '@xl:grid-cols-2')}
			{@render section('Performance', performanceFields, '@xl:grid-cols-2')}
			{@render section('Kerberos', kerberosFields, '@xl:grid-cols-2')}
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
									{restarts
										? 'Saving restarts NFS, which briefly interrupts connected clients.'
										: 'NFS is off, so saving only stores the settings.'}
								</span>
							</p>
						{:else}
							<p class="text-muted-foreground">
								{restarts
									? 'Saving restarts NFS.'
									: 'NFS is off, so saving only stores the settings.'}
							</p>
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

{#if previewOpen}
	<Preview {hostname} onClose={() => (previewOpen = false)} />
{/if}

{#if applyOpen}
	<ApplyDialog {hostname} onClose={() => (applyOpen = false)} onDone={refresh} />
{/if}
