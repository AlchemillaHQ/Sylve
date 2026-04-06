<script lang="ts">
	import { createFirewallNATRule, updateFirewallNATRule } from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { FirewallNATRule } from '$lib/types/network/firewall';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { handleAPIError } from '$lib/utils/http';
	import { validateFirewallNATRulePayload } from '$lib/utils/network/firewall';
	import { toast } from 'svelte-sonner';
	import { SvelteSet } from 'svelte/reactivity';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		natRules: FirewallNATRule[];
		objects: NetworkObject[];
		interfaces: Iface[];
		switches: SwitchList;
		wgClients?: WireGuardClient[];
		afterChange: () => void;
	}

	let {
		open = $bindable(),
		edit = false,
		id,
		natRules,
		objects,
		interfaces,
		switches,
		wgClients = [],
		afterChange
	}: Props = $props();

	const editingRule = $derived.by(() => {
		if (edit && id) return natRules.find((r) => r.id === id) ?? null;
		return null;
	});

	function resolveAddr(val: string): { raw: string; objId: number | null } {
		const trimmed = val.trim();
		if (!trimmed) return { raw: '', objId: null };
		const obj = objects.find(
			(o) => ['Host', 'Network', 'FQDN', 'List'].includes(o.type) && String(o.id) === trimmed
		);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	function resolveHostTarget(val: string): { raw: string; objId: number | null } {
		const trimmed = val.trim();
		if (!trimmed) return { raw: '', objId: null };
		const obj = objects.find((o) => ['Host', 'FQDN'].includes(o.type) && String(o.id) === trimmed);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	function resolvePort(val: string): { raw: string; objId: number | null } {
		const trimmed = val.trim();
		if (!trimmed) return { raw: '', objId: null };
		const obj = objects.find((o) => o.type === 'Port' && String(o.id) === trimmed);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	function addrToForm(raw: string | undefined, objId: number | null | undefined): string {
		if (objId) return String(objId);
		return raw ?? '';
	}

	type Form = {
		name: string;
		description: string;
		enabled: boolean;
		log: boolean;
		priority: number;
		natType: 'snat' | 'dnat' | 'binat';
		policyRoutingEnabled: boolean;
		policyRouteGateway: string;
		protocol: 'any' | 'tcp' | 'udp' | 'icmp';
		family: 'any' | 'inet' | 'inet6';
		ingressInterfaces: string[];
		egressInterfaces: string[];
		source: string;
		dest: string;
		translateMode: 'interface' | 'address';
		translateTo: string;
		dnatTarget: string;
		dstPort: string;
		redirectPort: string;
	};

	function defaultForm(): Form {
		return {
			name: '',
			description: '',
			enabled: true,
			log: false,
			priority: natRules.length + 1,
			natType: 'snat',
			policyRoutingEnabled: false,
			policyRouteGateway: '',
			protocol: 'any',
			family: 'any',
			ingressInterfaces: [],
			egressInterfaces: [],
			source: '',
			dest: '',
			translateMode: 'interface',
			translateTo: '',
			dnatTarget: '',
			dstPort: '',
			redirectPort: ''
		};
	}

	let form = $state(defaultForm());

	$effect(() => {
		if (open) {
			if (editingRule) {
				form = {
					name: editingRule.name,
					description: editingRule.description ?? '',
					enabled: editingRule.enabled ?? true,
					log: editingRule.log ?? false,
					priority: editingRule.priority,
					natType: editingRule.natType ?? 'snat',
					policyRoutingEnabled: editingRule.policyRoutingEnabled ?? false,
					policyRouteGateway: editingRule.policyRouteGateway ?? '',
					protocol: editingRule.protocol,
					family: editingRule.family ?? 'any',
					ingressInterfaces: editingRule.ingressInterfaces ?? [],
					egressInterfaces: editingRule.egressInterfaces ?? [],
					source: addrToForm(editingRule.sourceRaw, editingRule.sourceObjId),
					dest: addrToForm(editingRule.destRaw, editingRule.destObjId),
					translateMode: editingRule.translateMode ?? 'interface',
					translateTo: addrToForm(editingRule.translateToRaw, editingRule.translateToObjId),
					dnatTarget: addrToForm(editingRule.dnatTargetRaw, editingRule.dnatTargetObjId),
					dstPort: addrToForm(editingRule.dstPortsRaw, editingRule.dstPortObjId),
					redirectPort: addrToForm(editingRule.redirectPortsRaw, editingRule.redirectPortObjId)
				};
			} else {
				form = defaultForm();
			}
		}
	});

	let cbOpen = $state({
		ingressInterfaces: false,
		egressInterfaces: false,
		source: false,
		dest: false,
		translateTo: false,
		dnatTarget: false,
		dstPort: false,
		redirectPort: false
	});

	const natTypeOptions = [
		{ value: 'snat', label: 'SNAT' },
		{ value: 'dnat', label: 'DNAT' },
		{ value: 'binat', label: 'BINAT' }
	];

	const protocolOptions = [
		{ value: 'any', label: 'Any' },
		{ value: 'tcp', label: 'TCP' },
		{ value: 'udp', label: 'UDP' },
		{ value: 'icmp', label: 'ICMP' }
	];

	const familyOptions = [
		{ value: 'any', label: 'Any' },
		{ value: 'inet', label: 'IPv4' },
		{ value: 'inet6', label: 'IPv6' }
	];

	const translateModeOptions = [
		{ value: 'interface', label: 'Interface Address' },
		{ value: 'address', label: 'Specific Address' }
	];

	const ifaceOptions = $derived.by(() => {
		const opts: { label: string; value: string }[] = [];
		const covered = new SvelteSet<string>();
		for (const sw of switches.standard ?? []) {
			opts.push({ label: sw.name, value: sw.bridgeName });
			covered.add(sw.bridgeName);
		}
		for (const sw of switches.manual ?? []) {
			opts.push({ label: sw.name, value: sw.bridge });
			covered.add(sw.bridge);
		}
		for (const iface of interfaces) {
			if (covered.has(iface.name)) continue;
			let label = iface.description || iface.name;
			if (iface.name === 'wgs0') {
				label = 'WireGuard Server';
			} else {
				const wgcMatch = iface.name.match(/^wgc(\d+)$/);
				if (wgcMatch) {
					const clientId = Number(wgcMatch[1]);
					const client = wgClients.find((c) => c.id === clientId);
					if (client) label = `${client.name} (WG Client)`;
				}
			}
			opts.push({ label, value: iface.name });
		}
		return opts;
	});

	const addrObjectOptions = $derived(
		objects
			.filter((obj) => ['Host', 'Network', 'FQDN', 'List'].includes(obj.type))
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const hostTargetOptions = $derived(
		objects
			.filter((obj) => ['Host', 'FQDN'].includes(obj.type))
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const portObjectOptions = $derived(
		objects
			.filter((obj) => obj.type === 'Port')
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const showDNATFields = $derived(form.natType === 'dnat');
	const showSNATOrBINATFields = $derived(form.natType === 'snat' || form.natType === 'binat');
	const showTranslateTarget = $derived(showSNATOrBINATFields && form.translateMode === 'address');
	const supportsPorts = $derived(form.protocol === 'tcp' || form.protocol === 'udp');
	const showPolicyRoutingSettings = $derived(showSNATOrBINATFields);
	const enforceSingleEgressForPolicyRouting = $derived(
		showPolicyRoutingSettings && form.policyRoutingEnabled
	);

	$effect(() => {
		if (!showPolicyRoutingSettings) {
			form.policyRoutingEnabled = false;
			form.policyRouteGateway = '';
		}
		if (!form.policyRoutingEnabled) {
			form.policyRouteGateway = '';
		}
		if (enforceSingleEgressForPolicyRouting && form.egressInterfaces.length > 1) {
			form.egressInterfaces = [form.egressInterfaces[form.egressInterfaces.length - 1]];
		}
	});

	async function save() {
		if (!form.name.trim()) {
			toast.error('Rule name is required', { position: 'bottom-center' });
			return;
		}

		const src = resolveAddr(form.source);
		const dst = resolveAddr(form.dest);
		const translate = showTranslateTarget
			? resolveHostTarget(form.translateTo)
			: { raw: '', objId: null };
		const dnatTarget = showDNATFields
			? resolveHostTarget(form.dnatTarget)
			: { raw: '', objId: null };
		const dstPort =
			showDNATFields && supportsPorts ? resolvePort(form.dstPort) : { raw: '', objId: null };
		const redirectPort =
			showDNATFields && supportsPorts ? resolvePort(form.redirectPort) : { raw: '', objId: null };

		const payload = {
			name: form.name.trim(),
			description: form.description.trim(),
			enabled: form.enabled,
			log: form.log,
			priority: Number(form.priority),
			natType: form.natType,
			policyRoutingEnabled: showSNATOrBINATFields ? form.policyRoutingEnabled : false,
			policyRouteGateway:
				showSNATOrBINATFields && form.policyRoutingEnabled ? form.policyRouteGateway.trim() : '',
			protocol: form.protocol,
			family: form.family,
			ingressInterfaces:
				showDNATFields || (showSNATOrBINATFields && form.policyRoutingEnabled)
					? form.ingressInterfaces
					: [],
			egressInterfaces: showSNATOrBINATFields ? form.egressInterfaces : [],
			sourceRaw: src.raw,
			sourceObjId: src.objId,
			destRaw: dst.raw,
			destObjId: dst.objId,
			translateMode: showSNATOrBINATFields ? form.translateMode : '',
			translateToRaw: showTranslateTarget ? translate.raw : '',
			translateToObjId: showTranslateTarget ? translate.objId : null,
			dnatTargetRaw: showDNATFields ? dnatTarget.raw : '',
			dnatTargetObjId: showDNATFields ? dnatTarget.objId : null,
			dstPortsRaw: showDNATFields && supportsPorts ? dstPort.raw : '',
			dstPortObjId: showDNATFields && supportsPorts ? dstPort.objId : null,
			redirectPortsRaw: showDNATFields && supportsPorts ? redirectPort.raw : '',
			redirectPortObjId: showDNATFields && supportsPorts ? redirectPort.objId : null
		};

		const validation = validateFirewallNATRulePayload(payload);
		if (!validation.valid) {
			toast.error(validation.error ?? 'Invalid NAT rule', { position: 'bottom-center' });
			return;
		}

		const result =
			edit && id ? await updateFirewallNATRule(id, payload) : await createFirewallNATRule(payload);

		if (typeof result === 'number' || ('status' in result && result.status === 'success')) {
			toast.success(`NAT rule ${edit ? 'updated' : 'created'}`, { position: 'bottom-center' });
			afterChange();
			open = false;
			form = defaultForm();
		} else {
			handleAPIError(result);
			toast.error(`Failed to ${edit ? 'update' : 'create'} NAT rule`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[96%] overflow-hidden p-5 lg:max-w-3xl md:max-w-2xl">
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--swap-horizontal-circle-outline] h-5 w-5"></span>
						{#if editingRule}
							<span>Edit NAT Rule — {editingRule.name}</span>
						{:else}
							<span>Create NAT Rule</span>
						{/if}
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="link"
					class="h-4"
					title="Reset"
					onclick={() => (form = defaultForm())}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Reset</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title="Close" onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</div>
		</div>

		<ScrollArea orientation="vertical" class="h-[68vh] pr-2">
			<div class="space-y-5">
				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Basic Info
					</p>
					<div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
						<CustomValueInput
							label="Name"
							placeholder="Masquerade LAN"
							bind:value={form.name}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Description"
							placeholder="Optional description"
							bind:value={form.description}
							classes="space-y-1.5"
						/>
						<CustomValueInput
							label="Priority"
							placeholder="1"
							type="number"
							bind:value={form.priority}
							classes="space-y-1.5"
						/>
					</div>
					<div class="mt-3 flex flex-row gap-4">
						<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
						<CustomCheckbox label="Log" bind:checked={form.log} />
					</div>
				</section>

				<div class="border-t"></div>

				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Rule Settings
					</p>
					<div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
						<SimpleSelect
							label="NAT Type"
							options={natTypeOptions}
							bind:value={form.natType}
							onChange={(v) => (form.natType = v as Form['natType'])}
						/>
						<SimpleSelect
							label="Protocol"
							options={protocolOptions}
							bind:value={form.protocol}
							onChange={(v) => (form.protocol = v as Form['protocol'])}
						/>
						<SimpleSelect
							label="Family"
							options={familyOptions}
							bind:value={form.family}
							onChange={(v) => (form.family = v as Form['family'])}
						/>
					</div>
				</section>

				<div class="border-t"></div>

				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Directional Interfaces
					</p>
					{#if interfaces.length > 0}
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<ComboBox
								bind:open={cbOpen.ingressInterfaces}
								label="Ingress Interfaces"
								bind:value={form.ingressInterfaces}
								data={ifaceOptions}
								classes="space-y-1"
								placeholder={showSNATOrBINATFields
									? form.policyRoutingEnabled
										? 'Optional scope for policy routing'
										: 'Enable policy routing to scope ingress'
									: 'Any ingress interface'}
								disabled={showSNATOrBINATFields && !form.policyRoutingEnabled}
								width="w-full"
								multiple={true}
							/>
							<ComboBox
								bind:open={cbOpen.egressInterfaces}
								label="Egress Interfaces"
								bind:value={form.egressInterfaces}
								data={ifaceOptions}
								classes="space-y-1"
								placeholder={showDNATFields ? 'Not used for DNAT' : 'Any egress interface'}
								disabled={showDNATFields}
								width="w-full"
								multiple={true}
							/>
						</div>
						<p class="mt-2 text-xs text-muted-foreground">
							DNAT needs an ingress interface. SNAT/BINAT need an egress interface; ingress is
							optional (for policy routing scope).
						</p>
					{:else}
						<p class="text-xs text-muted-foreground">
							No interfaces available. Add network interfaces before creating NAT rules.
						</p>
					{/if}
				</section>

				<div class="border-t"></div>

				<section>
					<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						Source & Destination Match
					</p>
					<p class="mb-3 text-xs text-muted-foreground">
						Select a network object or type a raw IP / CIDR. Leave empty to match any.
					</p>
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<ComboBox
							bind:open={cbOpen.source}
							label="Source"
							bind:value={form.source}
							data={addrObjectOptions}
							classes="space-y-1"
							placeholder="any - object or 192.168.1.0/24"
							width="w-full"
							allowCustom={true}
						/>
						<ComboBox
							bind:open={cbOpen.dest}
							label="Destination"
							bind:value={form.dest}
							data={addrObjectOptions}
							classes="space-y-1"
							placeholder="any - object or 10.0.0.0/8"
							width="w-full"
							allowCustom={true}
						/>
					</div>
				</section>

				{#if showSNATOrBINATFields}
					<div class="border-t"></div>
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							Translation
						</p>
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<SimpleSelect
								label="Translate Mode"
								options={translateModeOptions}
								bind:value={form.translateMode}
								onChange={(v) => (form.translateMode = v as Form['translateMode'])}
							/>
							{#if showTranslateTarget}
								<ComboBox
									bind:open={cbOpen.translateTo}
									label="Translate To"
									bind:value={form.translateTo}
									data={hostTargetOptions}
									classes="space-y-1"
									placeholder="Host object or 198.51.100.50"
									width="w-full"
									allowCustom={true}
								/>
							{/if}
						</div>
						<p class="mt-2 text-xs text-muted-foreground">
							For interface mode, PF uses the selected egress interface address automatically.
						</p>
					</section>

					<div class="border-t"></div>
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							Policy Routing
						</p>
						<div class="space-y-3">
							<CustomCheckbox
								label="Enable policy routing"
								bind:checked={form.policyRoutingEnabled}
							/>
							{#if form.policyRoutingEnabled}
								<CustomValueInput
									label="Policy route gateway"
									placeholder={form.family === 'inet6' ? '2001:db8::1' : '198.51.100.1'}
									bind:value={form.policyRouteGateway}
									classes="space-y-1.5"
								/>
							{/if}
						</div>
						<p class="mt-2 text-xs text-muted-foreground">
							When enabled, exactly one egress interface and a gateway are required. Gateway must
							match the rule family.
						</p>
					</section>
				{/if}

				{#if showDNATFields}
					<div class="border-t"></div>
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							DNAT Target
						</p>
						<ComboBox
							bind:open={cbOpen.dnatTarget}
							label="Target Host"
							bind:value={form.dnatTarget}
							data={hostTargetOptions}
							classes="space-y-1"
							placeholder="Host object or 10.0.0.10"
							width="w-full"
							allowCustom={true}
						/>
					</section>
				{/if}

				{#if showDNATFields && supportsPorts}
					<div class="border-t"></div>
					<section>
						<p class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							DNAT Ports
						</p>
						<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<ComboBox
								bind:open={cbOpen.dstPort}
								label="Match Destination Port"
								bind:value={form.dstPort}
								data={portObjectOptions}
								classes="space-y-1"
								placeholder="Port object, 443, or 8000:8080"
								width="w-full"
								allowCustom={true}
							/>
							<ComboBox
								bind:open={cbOpen.redirectPort}
								label="Rewrite To Port"
								bind:value={form.redirectPort}
								data={portObjectOptions}
								classes="space-y-1"
								placeholder="Optional target port"
								width="w-full"
								allowCustom={true}
							/>
						</div>
					</section>
				{/if}
			</div>
		</ScrollArea>

		<Dialog.Footer class="pt-2">
			<div class="flex items-center gap-2">
				<Button size="sm" variant="outline" onclick={() => (open = false)}>Cancel</Button>
				<Button size="sm" onclick={save}>{edit ? 'Save' : 'Create'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
