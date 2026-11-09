<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		getCatalog,
		getCatalogIcon,
		getCatalogTemplate,
		renderCatalogApp
	} from '$lib/api/container/catalog';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type {
		CatalogApp,
		CatalogPlan,
		CatalogTemplate,
		CatalogVariable
	} from '$lib/types/container/catalog';
	import { getAPIErrorText, isAPIResponse } from '$lib/utils/http';
	import { describeContainerError } from '$lib/utils/container/errors';
	import { nextFreePort, portConflict } from '$lib/utils/container/ports';
	import { getMountableDatasets, getUsedPorts } from '$lib/api/container/resources';
	import { getSimpleContainers } from '$lib/api/container/container';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { resource } from 'runed';
	import { tick } from 'svelte';
	import CatalogIcon from './CatalogIcon.svelte';

	interface Props {
		open: boolean;
		node: string;
		onPick: (plan: CatalogPlan) => void;
	}

	let { open = $bindable(), node, onPick }: Props = $props();
	let content = $state<HTMLElement | null>(null);

	const catalog = resource(
		() => (open ? node : ''),
		async (hostname) => {
			if (!hostname) return null;
			const result = await getCatalog(hostname);
			if (isAPIResponse(result)) throw new Error(getAPIErrorText(result));
			return result.sort(
				(a, b) =>
					Number(a.os === 'linux') - Number(b.os === 'linux') || a.name.localeCompare(b.name)
			);
		}
	);

	const listHeight = 'h-[max(17.25rem,calc(85vh-14rem))]';

	let query = $state('');
	let category = $state('');
	let selected = $state<CatalogApp | null>(null);
	let categories = $derived(
		[...new Set((catalog.current ?? []).map((app) => app.category))].sort()
	);
	let apps = $derived(
		(catalog.current ?? []).filter(
			(app) =>
				(!category || app.category === category) &&
				`${app.name} ${app.description} ${app.category}`
					.toLowerCase()
					.includes(query.trim().toLowerCase())
		)
	);

	let template = $state<CatalogTemplate | null>(null);
	let settings = $state({
		variant: '',
		values: {} as Record<string, string>,
		choices: {} as Record<string, string>,
		stack: ''
	});
	let showAdvanced = $state(false);
	let busy = $state(false);
	let error = $state('');

	let chosen = $derived(
		(template?.choices ?? []).flatMap((choice) =>
			choice.options.filter((option) => option.id === settings.choices[choice.id])
		)
	);
	let asked = $derived(
		chosen
			.flatMap((option) => option.ask)
			.filter((variable) => !template?.variables.some((other) => other.name === variable.name))
	);
	let containers = $derived.by(() => {
		const drops = new Set(chosen.flatMap((option) => option.drops));
		const stack = template?.stack ?? '';
		const named = (name: string) =>
			!stack || !settings.stack
				? name
				: name === stack
					? settings.stack
					: name.startsWith(`${stack}-`)
						? settings.stack + name.slice(stack.length)
						: name;
		return [
			...(template?.containers ?? []).filter((name) => !drops.has(name)),
			...chosen.flatMap((option) => option.adds)
		].map(named);
	});

	const datasets = resource(
		() => (template?.variables.some((variable) => variable.target) ? node : ''),
		async (hostname) => (hostname ? await getMountableDatasets(hostname) : []),
		{ initialValue: [] }
	);
	const usedPorts = resource(
		() => (template ? node : ''),
		async (hostname) => (hostname ? await getUsedPorts(hostname) : []),
		{ initialValue: [] }
	);
	const otherPorts = (variable: CatalogVariable) =>
		(template?.variables ?? [])
			.filter(
				(other) =>
					other.type === 'port' &&
					other.name !== variable.name &&
					(other.protocol || 'tcp') === (variable.protocol || 'tcp')
			)
			.map((other) => Number(settings.values[other.name]));

	async function configure() {
		if (!selected || busy) return;
		busy = true;
		error = '';
		const result = await getCatalogTemplate(selected.source, selected.id, node);
		if (isAPIResponse(result)) {
			busy = false;
			error = getAPIErrorText(result);
			return;
		}
		const asks = result.choices.flatMap((choice) => choice.options.flatMap((option) => option.ask));
		let stack = result.stack;
		if (stack) {
			const list = await getSimpleContainers(node).catch(() => []);
			const taken = new Set(list.map((container) => container.stack));
			for (let copy = 2; taken.has(stack); copy++) stack = `${result.stack}-${copy}`;
		}
		settings = {
			variant: result.app.variants.find((variant) => variant.default)?.id ?? '',
			values: Object.fromEntries(
				[...asks, ...result.variables].map((variable) => [variable.name, variable.default])
			),
			choices: Object.fromEntries(result.choices.map((choice) => [choice.id, choice.default])),
			stack
		};
		showAdvanced = false;
		template = result;
		busy = false;
		await tick();
		content
			?.querySelector<HTMLElement>('input:not([type="checkbox"]), textarea')
			?.focus({ preventScroll: true });
	}

	async function use() {
		if (!template) return;
		busy = true;
		error = '';
		const values = Object.fromEntries(
			Object.entries(settings.values).map(([name, value]) => [name, String(value ?? '')])
		);
		const result = await renderCatalogApp(
			template.app.source,
			template.app.id,
			{ ...$state.snapshot(settings), values },
			node
		);
		busy = false;
		if (isAPIResponse(result)) {
			error = getAPIErrorText(result);
			return;
		}
		onPick(result);
		open = false;
	}

	async function back() {
		template = null;
		error = '';
		await tick();
		content?.querySelector('[aria-pressed="true"]')?.scrollIntoView({ block: 'center' });
	}

	const daemonlessLogo = resource(
		() => (open ? node : ''),
		async (hostname) => (hostname ? await getCatalogIcon('daemonless', '', hostname) : ''),
		{ initialValue: '' }
	);

	const provider = (app: CatalogApp) => ({
		name: 'daemonless',
		link: `daemonless.io/images/${app.id}`
	});

	const inputType = (variable: CatalogVariable) =>
		variable.type === 'secret' ? 'password' : variable.type === 'port' ? 'number' : 'text';

	const placeholders: Record<string, string> = { public_url: 'http://192.168.1.10:8080/' };
	const placeholder = (variable: CatalogVariable) =>
		variable.placeholder ||
		(variable.folders.length ? 'New datasets under the stack' : placeholders[variable.type]) ||
		(variable.optional ? 'Not set' : 'Required');
</script>

{#snippet linuxChip()}
	<Tooltip.Root>
		<Tooltip.Trigger>
			<span class="text-muted-foreground bg-muted rounded px-1.5 py-0.5 text-[11px] font-medium">
				Linux
			</span>
		</Tooltip.Trigger>
		<Tooltip.Content class="max-w-64">
			Runs through FreeBSD's Linux compatibility layer. Most apps work well; a few may not.
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet field(variable: CatalogVariable)}
	<div class="space-y-1.5">
		<div class="flex items-center justify-between gap-2">
			<Label
				class="min-w-0 gap-1 text-sm leading-snug"
				for="catalog-{variable.name}"
				title={variable.label || variable.name}
			>
				<span class="min-w-0 truncate">{variable.label || variable.name}</span>
				{#if variable.optional}
					<span class="text-muted-foreground shrink-0 font-normal">(Optional)</span>
				{/if}
			</Label>
			{#if variable.hint || variable.target || variable.folders.length}
				<Tooltip.Root>
					<Tooltip.Trigger aria-label="Help" class="inline-flex shrink-0 items-center">
						<span class="icon-[mdi--help-circle-outline] size-4"></span>
					</Tooltip.Trigger>
					<Tooltip.Content class="max-w-64 space-y-1 text-pretty break-words">
						{#if variable.hint}
							<p>{variable.hint}</p>
						{/if}
						{#if variable.target}
							<p>
								Mounted at {variable.target} in the container. Pick a ZFS dataset or type a host directory{variable.optional
									? ''
									: '; leave empty for a new volume'}.
							</p>
						{/if}
						{#if variable.folders.length}
							<p>
								Holds {variable.folders.join(', ')}. Leave empty for a ZFS dataset per folder under
								the stack; a host directory or dataset you give must already contain them.
							</p>
						{/if}
					</Tooltip.Content>
				</Tooltip.Root>
			{/if}
		</div>
		{#if variable.target}
			<CustomComboBox
				open={false}
				data={datasets.current.map((name) => ({ value: name, label: name }))}
				bind:value={settings.values[variable.name]}
				allowCustom
				placeholder={variable.optional ? 'Not mounted' : `New volume: ${variable.volume}`}
				width="w-full"
				classes=""
			/>
		{:else if Object.keys(variable.values).length > 0}
			<SimpleSelect
				options={Object.entries(variable.values).map(([value, label]) => ({ value, label }))}
				bind:value={settings.values[variable.name]}
				onChange={(value) => (settings.values[variable.name] = value)}
				classes={{ trigger: 'h-9 w-full' }}
			/>
		{:else}
			<Input
				id="catalog-{variable.name}"
				type={inputType(variable)}
				showPasswordOnFocus={variable.type === 'secret'}
				autocomplete={variable.type === 'secret' ? 'new-password' : 'off'}
				placeholder={placeholder(variable)}
				bind:value={settings.values[variable.name]}
			/>
		{/if}
		{#if variable.type === 'port'}
			{@const protocol = variable.protocol || 'tcp'}
			{@const port = Number(settings.values[variable.name])}
			{@const conflict = portConflict(usedPorts.current, protocol, port)}
			{#if conflict}
				{@const free = nextFreePort(usedPorts.current, protocol, port, otherPorts(variable))}
				<p class="flex items-start gap-1.5 text-xs text-amber-600 dark:text-amber-400">
					<span class="icon-[mdi--alert-circle-outline] mt-px h-3.5 w-3.5 shrink-0"></span>
					<span>
						{conflict}
						<button
							type="button"
							class="font-medium underline"
							onclick={() => (settings.values[variable.name] = String(free))}
						>
							Use {free}
						</button>
					</span>
				</p>
			{/if}
		{/if}
	</div>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		bind:ref={content}
		class="flex max-h-[85vh] flex-col gap-4 overflow-y-auto p-6 sm:max-w-3xl"
	>
		<Dialog.Header>
			<Dialog.Title>
				{#if template}
					<span class="flex items-center gap-3">
						<CatalogIcon
							source={template.app.source}
							id={template.app.id}
							name={template.app.name}
							{node}
						/>
						<span>Configure {template.app.name}</span>
					</span>
				{:else}
					<SpanWithIcon
						icon="icon-[mdi--view-grid-plus-outline]"
						size="h-5 w-5"
						gap="gap-2"
						title="Select Application"
					/>
				{/if}
			</Dialog.Title>
			<Dialog.Description>
				{template
					? template.app.description
					: 'Choose an application to deploy with Podman on Sylve'}
			</Dialog.Description>
		</Dialog.Header>

		{#if error}
			{@const problem = describeContainerError(error)}
			<Alert variant="destructive">
				<AlertTitle>{problem.title}</AlertTitle>
				{#if problem.detail}
					<AlertDescription class="whitespace-pre-wrap break-words">
						{problem.detail}
					</AlertDescription>
				{/if}
			</Alert>
		{/if}

		{#if !template}
			<div class="flex gap-2">
				<Input placeholder="Search applications" bind:value={query} class="h-9" />
				<SimpleSelect
					placeholder="All categories"
					options={[
						{ value: '', label: 'All categories' },
						...categories.map((name) => ({ value: name, label: name }))
					]}
					bind:value={category}
					onChange={(value) => (category = value)}
					classes={{ parent: 'w-56 shrink-0', trigger: 'h-9 w-full' }}
				/>
			</div>

			<ScrollArea orientation="vertical" class="shrink-0 {listHeight}">
				{#if apps.length === 0}
					<div class="text-muted-foreground flex items-center justify-center text-sm {listHeight}">
						{#if catalog.loading && !catalog.current}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Loading applications…
						{:else if catalog.error}
							{describeContainerError(catalog.error.message).title}
						{:else}
							No applications match.
						{/if}
					</div>
				{:else}
					<div class="grid grid-cols-1 gap-3 pr-3 sm:grid-cols-2">
						{#each apps as app (`${app.source}/${app.id}`)}
							{@const by = provider(app)}
							<button
								type="button"
								class="flex h-[8.25rem] flex-col overflow-hidden rounded-lg border text-left transition-colors {selected ===
								app
									? 'border-blue-600 bg-blue-600/5'
									: 'hover:border-muted-foreground/40'}"
								aria-pressed={selected === app}
								onclick={() => (selected = app)}
								ondblclick={() => {
									selected = app;
									configure();
								}}
							>
								<div class="flex min-h-0 min-w-0 flex-1 gap-3 p-4">
									<CatalogIcon source={app.source} id={app.id} name={app.name} {node} />
									<div class="min-w-0 flex-1">
										<div class="flex items-center gap-2">
											<span class="truncate font-medium">{app.name}</span>
											{#if app.os === 'linux'}
												{@render linuxChip()}
											{/if}
											{#if app.stack}
												<span
													class="text-muted-foreground bg-muted rounded px-1.5 py-0.5 text-[11px] font-medium"
													title="Runs as several containers"
												>
													Stack
												</span>
											{/if}
										</div>
										<p class="text-muted-foreground line-clamp-2 text-sm">{app.description}</p>
									</div>
								</div>
								<div
									class="text-muted-foreground bg-muted/40 flex shrink-0 items-center justify-between gap-2 border-t px-4 py-2 text-xs"
								>
									<span
										class="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-[10px]"
										title={by.name}
									>
										Provided by
										{#if daemonlessLogo.current}
											<img src={daemonlessLogo.current} alt={by.name} class="h-4 w-4" />
										{:else}
											{by.name}
										{/if}
									</span>
									<span class="min-w-0 truncate" title={by.link}>{by.link}</span>
								</div>
							</button>
						{/each}
					</div>
				{/if}
			</ScrollArea>
		{:else}
			<div class="min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
				{#if template.app.os === 'linux'}
					<p class="text-muted-foreground flex items-start gap-2 text-sm">
						<span class="icon-[mdi--information-outline] mt-0.5 h-4 w-4 shrink-0"></span>
						A Linux app, run through FreeBSD's Linux compatibility layer. Most apps work well; a few may
						not.
					</p>
				{/if}
				{#if containers.length > 1}
					<p class="text-muted-foreground flex items-start gap-2 text-sm">
						<span class="icon-[mdi--layers-outline] mt-0.5 h-4 w-4 shrink-0"></span>
						<span>
							Runs as {containers.length} containers on a network of their own: {containers.join(
								', '
							)}.
						</span>
					</p>
				{/if}

				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					{#if template.stack}
						<div class="space-y-1.5">
							<div class="flex items-center justify-between gap-2">
								<Label class="text-sm leading-snug" for="catalog-stack">Stack Name</Label>
								<Tooltip.Root>
									<Tooltip.Trigger aria-label="Help" class="inline-flex shrink-0 items-center">
										<span class="icon-[mdi--help-circle-outline] size-4"></span>
									</Tooltip.Trigger>
									<Tooltip.Content class="max-w-64 text-pretty">
										Names its containers, network and folders; a new name installs another copy.
									</Tooltip.Content>
								</Tooltip.Root>
							</div>
							<Input
								id="catalog-stack"
								autocomplete="off"
								placeholder={template.stack}
								bind:value={settings.stack}
							/>
						</div>
					{/if}
					{#if template.app.variants.length > 1}
						<SimpleSelect
							label="Variant"
							options={template.app.variants.map((variant) => ({
								value: variant.id,
								label: variant.label
							}))}
							bind:value={settings.variant}
							onChange={(value) => (settings.variant = value)}
							classes={{
								parent: 'space-y-1.5',
								label: 'flex h-5 items-center text-sm',
								trigger: 'h-9 w-full'
							}}
						/>
					{/if}
					{#each template.choices as choice (choice.id)}
						<div>
							<SimpleSelect
								label={choice.label}
								options={choice.options.map((option) => ({
									value: option.id,
									label: option.label
								}))}
								bind:value={settings.choices[choice.id]}
								onChange={(value) => (settings.choices[choice.id] = value)}
								classes={{
									parent: 'space-y-1.5',
									label: 'flex h-5 items-center text-sm',
									trigger: 'h-9 w-full'
								}}
							/>
							<p class="text-muted-foreground mt-1 text-xs">
								{choice.options.find((option) => option.id === settings.choices[choice.id])?.doc ??
									choice.doc}
							</p>
						</div>
					{/each}
					{#each template.variables.filter((variable) => !variable.advanced) as variable (variable.name)}
						{@render field(variable)}
					{/each}
					{#each asked as variable (variable.name)}
						{@render field(variable)}
					{/each}
				</div>

				{#if template.variables.some((variable) => variable.advanced)}
					<button
						type="button"
						class="text-muted-foreground hover:text-foreground flex items-center gap-1 text-sm"
						onclick={() => (showAdvanced = !showAdvanced)}
					>
						<span
							class="icon-[mdi--chevron-right] h-4 w-4 transition-transform {showAdvanced
								? 'rotate-90'
								: ''}"
						></span>
						Advanced settings
					</button>
					{#if showAdvanced}
						<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
							{#each template.variables.filter((variable) => variable.advanced) as variable (variable.name)}
								{@render field(variable)}
							{/each}
						</div>
					{/if}
				{/if}
			</div>
		{/if}

		<Dialog.Footer class="flex items-center gap-2">
			{#if template}
				<Button size="sm" variant="outline" class="h-8" onclick={back} disabled={busy}>Back</Button>
				<Button size="sm" class="h-8" onclick={use} disabled={busy}>
					{#if busy}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Use
					{/if}
				</Button>
			{:else}
				<Button size="sm" class="h-8" onclick={configure} disabled={!selected || busy}>
					{#if busy}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Next
					{/if}
				</Button>
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
