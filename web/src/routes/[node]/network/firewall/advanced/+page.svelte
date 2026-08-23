<script lang="ts">
	import {
		getFirewallAdvancedSettings,
		getRenderedConfigOnDisk,
		previewRenderedConfig,
		updateFirewallAdvancedSettings,
		type FirewallAdvancedRequest
	} from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { FirewallAdvancedSettings, RenderedConfig } from '$lib/types/network/firewall';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { cn } from '$lib/utils';
	import { toast } from 'svelte-sonner';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Data {
		advancedSettings: FirewallAdvancedSettings | APIResponse;
	}

	let { data }: { data: Data } = $props();

	function emptyForm(): FirewallAdvancedRequest {
		return {
			preRules: '',
			preNatDecl: '',
			postNatDecl: '',
			preTrafficAnchor: '',
			postTrafficAnchor: '',
			postRules: ''
		};
	}

	function formFromSettings(settings: FirewallAdvancedSettings): FirewallAdvancedRequest {
		return {
			preRules: settings.preRules ?? '',
			preNatDecl: settings.preNatDecl ?? '',
			postNatDecl: settings.postNatDecl ?? '',
			preTrafficAnchor: settings.preTrafficAnchor ?? '',
			postTrafficAnchor: settings.postTrafficAnchor ?? '',
			postRules: settings.postRules ?? ''
		};
	}

	function initialPageState(result: FirewallAdvancedSettings | APIResponse): {
		form: FirewallAdvancedRequest;
		error: APIResponse | null;
	} {
		if (isAPIResponse(result)) {
			return { form: emptyForm(), error: result };
		}
		return { form: formFromSettings(result), error: null };
	}

	// Route data intentionally initializes this editable draft once. Retries update it explicitly.
	// svelte-ignore state_referenced_locally
	const initialState = initialPageState(data.advancedSettings);
	let form = $state<FirewallAdvancedRequest>(initialState.form);
	let loadError = $state<APIResponse | null>(initialState.error);
	let loadingSettings = $state(false);

	type Section =
		| 'preRules'
		| 'preNatDecl'
		| 'postNatDecl'
		| 'preTrafficAnchor'
		| 'postTrafficAnchor'
		| 'postRules'
		| 'generated'
		| 'objectTables';

	const sections: { id: Section; label: string; icon: string; description: string }[] = [
		{
			id: 'preRules',
			label: 'Pre Rules',
			icon: 'mdi--arrow-up-bold-circle-outline',
			description:
				'Options, normalization, table definitions. Injected before all Sylve-managed content.'
		},
		{
			id: 'preNatDecl',
			label: 'Pre NAT Declarations',
			icon: 'mdi--arrow-up-bold-circle-outline',
			description:
				"Translation rules injected before Sylve's nat-anchor / rdr-anchor / binat-anchor lines."
		},
		{
			id: 'postNatDecl',
			label: 'Post NAT Declarations',
			icon: 'mdi--swap-horizontal-bold',
			description:
				"Injected after Sylve's translation anchors, before the filtering section. Transition zone for translation or filtering."
		},
		{
			id: 'preTrafficAnchor',
			label: 'Pre Traffic Anchor',
			icon: 'mdi--arrow-up-bold-circle-outline',
			description: 'Filtering rules injected before Sylve\'s anchor "sylve/traffic-rules" line.'
		},
		{
			id: 'postTrafficAnchor',
			label: 'Post Traffic Anchor',
			icon: 'mdi--arrow-down-bold-circle-outline',
			description: "Filtering rules injected after Sylve's traffic anchor, before Post Rules."
		},
		{
			id: 'postRules',
			label: 'Post Rules',
			icon: 'mdi--arrow-down-bold-circle-outline',
			description: 'Final filtering rules injected after all Sylve-managed content.'
		}
	];

	const previewSections: { id: Section; label: string; icon: string; description: string }[] = [
		{
			id: 'generated',
			label: 'Generated pf.conf',
			icon: 'mdi--file-document-outline',
			description: 'Full rendered pf.conf that will be written to /etc/pf.conf.'
		},
		{
			id: 'objectTables',
			label: 'Object Tables',
			icon: 'mdi--database',
			description: 'Rendered object table definitions from the firewall objects.'
		}
	];

	let activeSection: Section = $state('preRules');
	let previewTab: 'preview' | 'disk' = $state('preview');
	let renderedPreview = $state.raw<RenderedConfig | null>(null);
	let renderedDisk = $state.raw<RenderedConfig | null>(null);
	let previewError = $state<string | null>(null);
	let diskError = $state<string | null>(null);
	let previewing = $state(false);
	let loadingDisk = $state(false);
	let saving = $state(false);
	let previewRequestVersion = 0;
	let diskRequestVersion = 0;

	function requestFromForm(): FirewallAdvancedRequest {
		return {
			preRules: form.preRules ?? '',
			preNatDecl: form.preNatDecl ?? '',
			postNatDecl: form.postNatDecl ?? '',
			preTrafficAnchor: form.preTrafficAnchor ?? '',
			postTrafficAnchor: form.postTrafficAnchor ?? '',
			postRules: form.postRules ?? ''
		};
	}

	function normalizedRequest(request: FirewallAdvancedRequest): FirewallAdvancedRequest {
		return {
			preRules: request.preRules.trim(),
			preNatDecl: request.preNatDecl.trim(),
			postNatDecl: request.postNatDecl.trim(),
			preTrafficAnchor: request.preTrafficAnchor.trim(),
			postTrafficAnchor: request.postTrafficAnchor.trim(),
			postRules: request.postRules.trim()
		};
	}

	function responseDetail(result: APIResponse): string {
		if (
			typeof result.data === 'object' &&
			result.data !== null &&
			'detail' in result.data &&
			typeof result.data.detail === 'string'
		) {
			return result.data.detail;
		}
		if (typeof result.error === 'string' && result.error) return result.error;
		return result.message || 'The request could not be completed.';
	}

	function markPreviewStale() {
		previewRequestVersion += 1;
		previewing = false;
		renderedPreview = null;
		previewError = null;
	}

	function markDiskStale() {
		diskRequestVersion += 1;
		loadingDisk = false;
		renderedDisk = null;
		diskError = null;
	}

	async function reloadSettings() {
		if (loadingSettings) return;
		loadingSettings = true;
		try {
			const result = await getFirewallAdvancedSettings();
			if (isAPIResponse(result)) {
				loadError = result;
				handleAPIError(result);
				return;
			}

			form = formFromSettings(result);
			loadError = null;
			markPreviewStale();
			markDiskStale();
		} finally {
			loadingSettings = false;
		}
	}

	async function loadPreview() {
		if (previewing || loadError) return;
		const requestVersion = ++previewRequestVersion;
		previewing = true;
		previewError = null;
		try {
			const result = await previewRenderedConfig(requestFromForm());
			if (requestVersion !== previewRequestVersion) return;
			if (isAPIResponse(result)) {
				renderedPreview = null;
				previewError = responseDetail(result);
				handleAPIError(result);
				return;
			}
			renderedPreview = result;
		} finally {
			if (requestVersion === previewRequestVersion) previewing = false;
		}
	}

	async function loadDisk() {
		if (loadingDisk || loadError) return;
		const requestVersion = ++diskRequestVersion;
		loadingDisk = true;
		diskError = null;
		try {
			const result = await getRenderedConfigOnDisk();
			if (requestVersion !== diskRequestVersion) return;
			if (isAPIResponse(result)) {
				renderedDisk = null;
				diskError = responseDetail(result);
				handleAPIError(result);
				return;
			}
			renderedDisk = result;
		} finally {
			if (requestVersion === diskRequestVersion) loadingDisk = false;
		}
	}

	function previewContent(): string {
		if (previewTab === 'preview' && previewing) return 'Generating preview...';
		if (previewTab === 'disk' && loadingDisk) return 'Loading on-disk configuration...';
		if (previewTab === 'preview' && previewError) return previewError;
		if (previewTab === 'disk' && diskError) return diskError;
		if (activeSection === 'generated') {
			const cfg = previewTab === 'preview' ? renderedPreview : renderedDisk;
			return cfg?.pfConf ?? 'No generated config available.';
		}
		if (activeSection === 'objectTables') {
			const cfg = previewTab === 'preview' ? renderedPreview : renderedDisk;
			return cfg?.objectTables ?? 'No object tables available.';
		}
		return '';
	}

	function selectSection(section: Section) {
		activeSection = section;
		if (activeSection === 'generated' || activeSection === 'objectTables') {
			if (previewTab === 'preview' && !renderedPreview) {
				void loadPreview();
			}
			if (previewTab === 'disk' && !renderedDisk) {
				void loadDisk();
			}
		}
	}

	function selectPreviewTab(tab: 'preview' | 'disk') {
		previewTab = tab;
		if (tab === 'preview') {
			void loadPreview();
		} else {
			void loadDisk();
		}
	}

	async function saveAdvancedSettings() {
		if (saving || loadError) return;
		saving = true;
		const request = requestFromForm();
		try {
			const result = await updateFirewallAdvancedSettings(request);
			if (result.status === 'success') {
				form = normalizedRequest(request);
				markPreviewStale();
				markDiskStale();
				toast.success('Advanced firewall settings updated', { position: 'bottom-center' });
				return;
			}
			handleAPIError(result);
		} finally {
			saving = false;
		}
	}

	function isEditable(section: Section): boolean {
		return !['generated', 'objectTables'].includes(section);
	}
</script>

<div class="flex h-full w-full flex-col gap-4 p-4">
	<div class="rounded-lg border bg-card p-4">
		<div class="flex items-center gap-3">
			<div class="bg-primary/10 flex h-10 w-10 shrink-0 items-center justify-center rounded-md">
				<span class="icon-[mdi--shield-edit] text-primary h-5 w-5"></span>
			</div>
			<div>
				<h2 class="text-lg font-semibold">Advanced Firewall Settings</h2>
				<p class="text-muted-foreground text-sm">
					Global PF rules injected at specific positions in
					<code class="bg-muted rounded px-1 py-0.5 font-mono text-xs">/etc/pf.conf</code>.
				</p>
			</div>
		</div>
		<div
			class="border-amber-500/30 bg-amber-500/5 text-amber-600 dark:text-amber-400 mt-4 flex items-start gap-2 rounded-md border px-3 py-2 text-sm"
		>
			<span class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0"></span>
			<span>Rules here are applied globally and may override managed rules. Use with caution.</span>
		</div>
	</div>

	{#if loadError}
		<div
			class="border-destructive/30 bg-destructive/5 text-destructive flex items-center justify-between gap-4 rounded-lg border px-4 py-3"
			role="alert"
		>
			<div class="flex min-w-0 items-start gap-2">
				<span class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0"></span>
				<div>
					<p class="text-sm font-medium">Advanced firewall settings could not be loaded.</p>
					<p class="text-muted-foreground text-xs">
						Editing and saving are disabled so an empty form cannot overwrite the current rules.
					</p>
				</div>
			</div>
			<Button
				size="sm"
				variant="outline"
				onclick={reloadSettings}
				disabled={loadingSettings}
				aria-busy={loadingSettings}
			>
				{#if loadingSettings}
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					Retrying...
				{:else}
					<span class="icon-[mdi--refresh] h-4 w-4"></span>
					Retry
				{/if}
			</Button>
		</div>
	{/if}

	<div class="flex flex-1 gap-4 overflow-hidden">
		<!-- Sidebar -->
		<nav
			class="bg-card border-border flex w-56 shrink-0 flex-col gap-1 rounded-lg border p-2"
			aria-label="Advanced settings sections"
		>
			<div class="text-muted-foreground mb-1 px-2 text-xs font-medium uppercase tracking-wider">
				<!-- Editable Sections -->
				<SpanWithIcon
					icon="icon-[mdi--pencil]"
					title="Editable Sections"
					size="w-4 h-4"
					gap="gap-2"
				/>
			</div>
			{#each sections as section (section.id)}
				<button
					onclick={() => selectSection(section.id)}
					disabled={loadError !== null}
					class={cn(
						'flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50',
						activeSection === section.id
							? 'bg-muted dark:bg-muted font-medium'
							: 'hover:bg-muted dark:hover:bg-muted'
					)}
				>
					<span class="icon-[{section.icon}] h-4 w-4 shrink-0 text-blue-500"></span>
					<span class="truncate">{section.label}</span>
				</button>
			{/each}

			<div
				class="text-muted-foreground mb-1 mt-3 px-2 text-xs font-medium uppercase tracking-wider"
			>
				Preview
			</div>
			{#each previewSections as section (section.id)}
				<button
					onclick={() => selectSection(section.id)}
					disabled={loadError !== null}
					class={cn(
						'flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50',
						activeSection === section.id
							? 'bg-muted dark:bg-muted font-medium'
							: 'hover:bg-muted dark:hover:bg-muted'
					)}
				>
					<span class="icon-[{section.icon}] h-4 w-4 shrink-0 text-purple-500"></span>
					<span class="truncate">{section.label}</span>
				</button>
			{/each}
		</nav>

		<!-- Main Content -->
		<div class="flex min-w-0 flex-1 flex-col min-h-0 gap-4">
			{#if isEditable(activeSection)}
				{@const sec = sections.find((s) => s.id === activeSection)!}
				<div class="rounded-lg border bg-card flex flex-col flex-1 min-h-0 p-4">
					<div class="flex items-center gap-2 text-sm font-medium">
						<span class="icon-[{sec.icon}] h-4 w-4 text-blue-500"></span>
						{sec.label}
					</div>
					<p class="text-muted-foreground mb-3 text-xs">{sec.description}</p>
					<CustomValueInput
						label=""
						placeholder="# Enter {sec.label.toLowerCase()} here"
						bind:value={form[activeSection as keyof typeof form]}
						classes="flex-1 flex flex-col min-h-0"
						type="textarea"
						textAreaClasses="flex-1 min-h-0 font-mono text-xs resize-y"
						disabled={saving || loadError !== null}
						onChange={markPreviewStale}
					/>
				</div>
			{:else}
				{@const sec = previewSections.find((s) => s.id === activeSection)!}
				<div class="rounded-lg border bg-card flex flex-col flex-1 min-h-0 overflow-hidden">
					<div class="flex items-center justify-between gap-2 px-3 pt-3">
						<div class="flex items-center gap-2 text-sm font-medium">
							<span class="icon-[{sec.icon}] h-4 w-4 text-purple-500"></span>
							{sec.label}
						</div>
						<div class="bg-muted flex items-center gap-1 rounded-md p-0.5">
							<button
								onclick={() => selectPreviewTab('preview')}
								disabled={previewing || loadError !== null}
								aria-busy={previewing}
								class={cn(
									'rounded-md px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50',
									previewTab === 'preview'
										? 'bg-card text-foreground shadow-sm'
										: 'text-muted-foreground hover:text-foreground'
								)}
							>
								{#if previewing}
									<span class="icon-[mdi--loading] mr-1 inline-block h-3 w-3 animate-spin"></span>
									Generating...
								{:else}
									Preview
								{/if}
							</button>
							<button
								onclick={() => selectPreviewTab('disk')}
								disabled={loadingDisk || loadError !== null}
								aria-busy={loadingDisk}
								class={cn(
									'rounded-md px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50',
									previewTab === 'disk'
										? 'bg-card text-foreground shadow-sm'
										: 'text-muted-foreground hover:text-foreground'
								)}
							>
								{#if loadingDisk}
									<span class="icon-[mdi--loading] mr-1 inline-block h-3 w-3 animate-spin"></span>
									Loading...
								{:else}
									On Disk
								{/if}
							</button>
						</div>
					</div>
					<p class="text-muted-foreground mb-2 mt-1 px-3 text-xs">{sec.description}</p>
					<pre
						class="flex-1 min-h-0 w-full overflow-auto whitespace-pre-wrap break-all p-3 font-mono text-xs">{previewContent()}</pre>
				</div>
			{/if}
		</div>
	</div>

	<div class="flex items-center justify-between rounded-lg border bg-card p-4">
		<p class="text-muted-foreground flex items-center gap-1.5 text-xs">
			<span class="icon-[mdi--information-outline] h-3.5 w-3.5"></span>
			Changes take effect immediately upon saving.
		</p>
		<Button
			size="sm"
			onclick={saveAdvancedSettings}
			class="gap-1.5"
			disabled={saving || loadingSettings || loadError !== null}
			aria-busy={saving}
		>
			{#if saving}
				<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
				Saving...
			{:else}
				<span class="icon-[mdi--content-save-outline] h-4 w-4"></span>
				Save Settings
			{/if}
		</Button>
	</div>
</div>
