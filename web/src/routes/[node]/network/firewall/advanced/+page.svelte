<script lang="ts">
	import {
		getFirewallAdvancedSettings,
		getRenderedConfigOnDisk,
		previewRenderedConfig,
		updateFirewallAdvancedSettings
	} from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { FirewallAdvancedSettings, RenderedConfig } from '$lib/types/network/firewall';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { cn } from '$lib/utils';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		advancedSettings: FirewallAdvancedSettings | APIResponse;
	}

	let { data }: { data: Data } = $props();

	const advancedResource = resource(
		() => 'firewall-advanced-settings',
		async (key) => {
			const result = await getFirewallAdvancedSettings();
			updateCache(key, result);
			return result;
		},
		{
			initialValue:
				typeof data.advancedSettings === 'object' &&
				data.advancedSettings !== null &&
				'id' in data.advancedSettings
					? (data.advancedSettings as FirewallAdvancedSettings)
					: ({
							id: 0,
							preRules: '',
							preNatDecl: '',
							postNatDecl: '',
							preTrafficAnchor: '',
							postTrafficAnchor: '',
							postRules: '',
							createdAt: new Date().toISOString(),
							updatedAt: new Date().toISOString()
						} as FirewallAdvancedSettings)
		}
	);

	let form = $state({
		preRules: (advancedResource.current as FirewallAdvancedSettings).preRules ?? '',
		preNatDecl: (advancedResource.current as FirewallAdvancedSettings).preNatDecl ?? '',
		postNatDecl: (advancedResource.current as FirewallAdvancedSettings).postNatDecl ?? '',
		preTrafficAnchor: (advancedResource.current as FirewallAdvancedSettings).preTrafficAnchor ?? '',
		postTrafficAnchor:
			(advancedResource.current as FirewallAdvancedSettings).postTrafficAnchor ?? '',
		postRules: (advancedResource.current as FirewallAdvancedSettings).postRules ?? ''
	});

	$effect(() => {
		const s = advancedResource.current as FirewallAdvancedSettings;
		form.preRules = s.preRules ?? '';
		form.preNatDecl = s.preNatDecl ?? '';
		form.postNatDecl = s.postNatDecl ?? '';
		form.preTrafficAnchor = s.preTrafficAnchor ?? '';
		form.postTrafficAnchor = s.postTrafficAnchor ?? '';
		form.postRules = s.postRules ?? '';
	});

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
	let renderedPreview: RenderedConfig | null = $state(null);
	let renderedDisk: RenderedConfig | null = $state(null);

	async function loadPreview() {
		const result = await previewRenderedConfig({
			preRules: form.preRules ?? '',
			preNatDecl: form.preNatDecl ?? '',
			postNatDecl: form.postNatDecl ?? '',
			preTrafficAnchor: form.preTrafficAnchor ?? '',
			postTrafficAnchor: form.postTrafficAnchor ?? '',
			postRules: form.postRules ?? ''
		});
		if ('pfConf' in result) {
			renderedPreview = result;
		} else {
			renderedPreview = null;
		}
	}

	async function loadDisk() {
		const result = await getRenderedConfigOnDisk();
		if ('pfConf' in result) {
			renderedDisk = result;
		} else {
			renderedDisk = null;
		}
	}

	function previewContent(): string {
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

	$effect(() => {
		if (activeSection === 'generated' || activeSection === 'objectTables') {
			if (previewTab === 'preview' && !renderedPreview) {
				loadPreview();
			}
			if (previewTab === 'disk' && !renderedDisk) {
				loadDisk();
			}
		}
	});

	async function saveAdvancedSettings() {
		const result = await updateFirewallAdvancedSettings({
			preRules: form.preRules ?? '',
			preNatDecl: form.preNatDecl ?? '',
			postNatDecl: form.postNatDecl ?? '',
			preTrafficAnchor: form.preTrafficAnchor ?? '',
			postTrafficAnchor: form.postTrafficAnchor ?? '',
			postRules: form.postRules ?? ''
		});

		if (result.status === 'success') {
			toast.success('Advanced firewall settings updated', { position: 'bottom-center' });
			await advancedResource.refetch();
			renderedDisk = null;
		} else {
			handleAPIError(result);
			toast.error('Failed to update advanced settings', { position: 'bottom-center' });
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

	<div class="flex flex-1 gap-4 overflow-hidden">
		<!-- Sidebar -->
		<nav
			class="bg-card border-border flex w-56 shrink-0 flex-col gap-1 rounded-lg border p-2"
			aria-label="Advanced settings sections"
		>
			<div class="text-muted-foreground mb-1 px-2 text-xs font-medium uppercase tracking-wider">
				Editable Sections
			</div>
			{#each sections as section (section.id)}
				<button
					onclick={() => {
						activeSection = section.id;
					}}
					class={cn(
						'flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
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
					onclick={() => {
						activeSection = section.id;
					}}
					class={cn(
						'flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
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
								onclick={() => {
									previewTab = 'preview';
									loadPreview();
								}}
								class={cn(
									'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
									previewTab === 'preview'
										? 'bg-card text-foreground shadow-sm'
										: 'text-muted-foreground hover:text-foreground'
								)}>Preview</button
							>
							<button
								onclick={() => {
									previewTab = 'disk';
									loadDisk();
								}}
								class={cn(
									'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
									previewTab === 'disk'
										? 'bg-card text-foreground shadow-sm'
										: 'text-muted-foreground hover:text-foreground'
								)}>On Disk</button
							>
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
		<Button size="sm" onclick={saveAdvancedSettings} class="gap-1.5">
			<span class="icon-[mdi--content-save-outline] h-4 w-4"></span>
			Save Settings
		</Button>
	</div>
</div>
