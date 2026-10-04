<script lang="ts">
	import {
		createFirewallNATRule,
		updateFirewallNATRule,
		type FirewallNATRuleUpsertRequest
	} from '$lib/api/network/firewall';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { FirewallNATRule } from '$lib/types/network/firewall';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { handleAPIError } from '$lib/utils/http';
	import {
		validateFirewallNATRulePayload,
		firewallValidationDetail
	} from '$lib/utils/network/firewall';
	import { buildHostInterfaceOptions } from '$lib/utils/network/helpers';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		natRules: FirewallNATRule[];
		objects: NetworkObject[];
		interfaces: Iface[];
		switches: SwitchList;
		wgClients?: WireGuardClient[];
		afterChange: () => void | Promise<void>;
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
		const obj = objects.find((o) => isTranslationTarget(o) && String(o.id) === trimmed);
		if (obj) return { raw: '', objId: obj.id };
		return { raw: trimmed, objId: null };
	}

	function isTranslationTarget(object: NetworkObject): boolean {
		return (object.type === 'Host' && object.entries?.length === 1) || object.type === 'FQDN';
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
		passRedirectedTraffic: boolean;
		targetAddressScope: 'all' | 'private' | 'public';
		targetHandling: 'single' | 'round_robin';
		policyRoutingEnabled: boolean;
		policyRouteGateway: string;
		protocol: 'any' | 'tcp' | 'udp' | 'tcp_udp' | 'icmp' | 'icmp6';
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
		const maxPriority = natRules.reduce((maximum, rule) => Math.max(maximum, rule.priority), 0);
		return {
			name: '',
			description: '',
			enabled: true,
			log: false,
			priority: maxPriority + 1,
			natType: 'snat',
			passRedirectedTraffic: false,
			targetAddressScope: 'all',
			targetHandling: 'single',
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

	function formForRule(rule: FirewallNATRule | null): Form {
		if (!rule) return defaultForm();
		return {
			name: rule.name,
			description: rule.description ?? '',
			enabled: rule.enabled ?? true,
			log: rule.log ?? false,
			priority: rule.priority,
			natType: rule.natType ?? 'snat',
			passRedirectedTraffic: rule.passRedirectedTraffic ?? false,
			targetAddressScope: rule.targetAddressScope ?? 'all',
			targetHandling: rule.targetHandling ?? 'single',
			policyRoutingEnabled: rule.policyRoutingEnabled ?? false,
			policyRouteGateway: rule.policyRouteGateway ?? '',
			protocol: rule.protocol,
			family: rule.family ?? 'any',
			ingressInterfaces: [...(rule.ingressInterfaces ?? [])],
			egressInterfaces: [...(rule.egressInterfaces ?? [])],
			source: addrToForm(rule.sourceRaw, rule.sourceObjId),
			dest: addrToForm(rule.destRaw, rule.destObjId),
			translateMode: rule.translateMode ?? 'interface',
			translateTo: addrToForm(rule.translateToRaw, rule.translateToObjId),
			dnatTarget: addrToForm(rule.dnatTargetRaw, rule.dnatTargetObjId),
			dstPort: addrToForm(rule.dstPortsRaw, rule.dstPortObjId),
			redirectPort: addrToForm(rule.redirectPortsRaw, rule.redirectPortObjId)
		};
	}

	// This dialog is conditionally mounted, so the form intentionally snapshots its opening rule.
	// svelte-ignore state_referenced_locally
	let form = $state(formForRule(editingRule));
	let saving = $state(false);
	let validationError = $state<string | null>(null);

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
		{ value: 'tcp_udp', label: 'TCP/UDP' },
		{ value: 'icmp', label: 'ICMPv4' },
		{ value: 'icmp6', label: 'ICMPv6' }
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
	const addressScopeOptions = [
		{ value: 'all', label: 'All Addresses' },
		{ value: 'private', label: 'Private Only' },
		{ value: 'public', label: 'Public Only' }
	];
	const targetHandlingOptions = [
		{ value: 'single', label: 'Single Address' },
		{ value: 'round_robin', label: 'Round-robin Pool' }
	];

	const ifaceOptions = $derived.by(() => {
		return buildHostInterfaceOptions({
			interfaces,
			switches,
			getInterfaceLabel: (iface) => {
				if (iface.name === 'wgs0') return 'WireGuard Server (wgs0)';
				const wgcMatch = iface.name.match(/^wgc(\d+)$/);
				if (wgcMatch) {
					const client = wgClients.find((candidate) => candidate.id === Number(wgcMatch[1]));
					if (client) return `${client.name} (WG Client · ${iface.name})`;
				}
				return iface.description ? `${iface.description} (${iface.name})` : iface.name;
			}
		});
	});

	const addrObjectOptions = $derived(
		objects
			.filter((obj) => ['Host', 'Network', 'FQDN', 'List'].includes(obj.type))
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const hostTargetOptions = $derived(
		objects.filter(isTranslationTarget).map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const portObjectOptions = $derived(
		objects
			.filter((obj) => obj.type === 'Port')
			.map((obj) => ({ label: obj.name, value: String(obj.id) }))
	);

	const showDNATFields = $derived(form.natType === 'dnat');
	const showSNATOrBINATFields = $derived(form.natType === 'snat' || form.natType === 'binat');
	const showTranslateTarget = $derived(showSNATOrBINATFields && form.translateMode === 'address');
	const supportsPorts = $derived(
		form.protocol === 'tcp' || form.protocol === 'udp' || form.protocol === 'tcp_udp'
	);
	const targetIsFQDN = $derived.by(() => {
		if (!showDNATFields && !showTranslateTarget) return false;
		const value = showDNATFields ? form.dnatTarget : form.translateTo;
		return objects.some((object) => object.type === 'FQDN' && String(object.id) === value);
	});
	const enforceSingleEgressForPolicyRouting = $derived(
		showSNATOrBINATFields && form.policyRoutingEnabled
	);

	$effect(() => {
		if (!showSNATOrBINATFields) {
			form.policyRoutingEnabled = false;
		}
		if (!form.policyRoutingEnabled) {
			form.policyRouteGateway = '';
		}
		if (enforceSingleEgressForPolicyRouting && form.egressInterfaces.length > 1) {
			form.egressInterfaces = [form.egressInterfaces[form.egressInterfaces.length - 1]];
		}
	});

	function resetForm() {
		if (saving) return;
		form = formForRule(editingRule);
		validationError = null;
	}

	async function save() {
		if (saving) return;
		validationError = null;

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
		const policyRoutingEnabled = showSNATOrBINATFields && form.policyRoutingEnabled;

		const payload: FirewallNATRuleUpsertRequest = {
			name: form.name.trim(),
			description: form.description.trim(),
			enabled: form.enabled,
			log: form.log,
			priority: Number(form.priority),
			natType: form.natType,
			passRedirectedTraffic: showDNATFields && form.passRedirectedTraffic,
			targetAddressScope: targetIsFQDN ? form.targetAddressScope : 'all',
			targetHandling: targetIsFQDN && form.natType !== 'binat' ? form.targetHandling : 'single',
			policyRoutingEnabled,
			policyRouteGateway: policyRoutingEnabled ? form.policyRouteGateway.trim() : '',
			protocol: form.protocol,
			family: form.family,
			ingressInterfaces: showDNATFields || policyRoutingEnabled ? form.ingressInterfaces : [],
			egressInterfaces: showSNATOrBINATFields ? form.egressInterfaces : [],
			sourceRaw: src.raw,
			sourceObjId: src.objId,
			destRaw: dst.raw,
			destObjId: dst.objId,
			translateMode: showSNATOrBINATFields ? form.translateMode : undefined,
			translateToRaw: translate.raw,
			translateToObjId: translate.objId,
			dnatTargetRaw: dnatTarget.raw,
			dnatTargetObjId: dnatTarget.objId,
			dstPortsRaw: dstPort.raw,
			dstPortObjId: dstPort.objId,
			redirectPortsRaw: redirectPort.raw,
			redirectPortObjId: redirectPort.objId
		};

		const validation = validateFirewallNATRulePayload(payload);
		if (!validation.valid) {
			toast.error(validation.error ?? 'Invalid NAT rule', { position: 'bottom-center' });
			return;
		}

		saving = true;
		try {
			const result =
				edit && id
					? await updateFirewallNATRule(id, payload)
					: await createFirewallNATRule(payload);

			if (typeof result === 'number' || ('status' in result && result.status === 'success')) {
				await afterChange();
				toast.success(`NAT rule ${edit ? 'updated' : 'created'}`, {
					position: 'bottom-center'
				});
				form = defaultForm();
				open = false;
				return;
			}

			handleAPIError(result);
			validationError = firewallValidationDetail(result.data);
			toast.error(`Failed to ${edit ? 'update' : 'create'} NAT rule`, {
				position: 'bottom-center'
			});
		} catch (error) {
			console.error('Failed to save firewall NAT rule', error);
			toast.error(`Failed to ${edit ? 'update' : 'create'} NAT rule`, {
				position: 'bottom-center'
			});
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] overflow-hidden p-5 lg:max-w-3xl md:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={resetForm}
		onClose={() => {
			if (!saving) open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--swap-horizontal-circle-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title={editingRule ? `Edit NAT Rule — ${editingRule.name}` : 'Create NAT Rule'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<ScrollArea orientation="vertical" class="h-[68vh] pr-2">
			<div class="space-y-5">
				{#if validationError}
					<p role="alert" class="whitespace-pre-wrap break-words text-xs text-destructive">
						{validationError}
					</p>
				{/if}
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
					<div class="mt-3 flex flex-row flex-wrap gap-4">
						<CustomCheckbox label="Enabled" bind:checked={form.enabled} />
						<CustomCheckbox label="Log" bind:checked={form.log} />
						{#if showDNATFields}
							<CustomCheckbox
								label="Pass Redirected Traffic"
								bind:checked={form.passRedirectedTraffic}
							/>
						{/if}
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
							onChange={(v) => {
								form.protocol = v as Form['protocol'];
								if (v === 'icmp') form.family = 'inet';
								if (v === 'icmp6') form.family = 'inet6';
								if (v !== 'tcp' && v !== 'udp' && v !== 'tcp_udp') {
									form.dstPort = '';
									form.redirectPort = '';
								}
							}}
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
						Select an object or enter an IP, CIDR, or (interface). Leave empty to match any.
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
									placeholder="Host/FQDN object, IP, or (igb0)"
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
							placeholder="Host/FQDN object, IP, or (igb1.3)"
							width="w-full"
							allowCustom={true}
						/>
					</section>
				{/if}

				{#if targetIsFQDN}
					<section class="grid grid-cols-1 gap-3 sm:grid-cols-2">
						<SimpleSelect
							label="Address Scope"
							options={addressScopeOptions}
							bind:value={form.targetAddressScope}
							onChange={(v) => (form.targetAddressScope = v as Form['targetAddressScope'])}
						/>
						{#if form.natType !== 'binat'}
							<SimpleSelect
								label="Target Handling"
								options={targetHandlingOptions}
								bind:value={form.targetHandling}
								onChange={(v) => (form.targetHandling = v as Form['targetHandling'])}
							/>
						{/if}
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
				<Button size="sm" variant="outline" onclick={() => (open = false)} disabled={saving}
					>Cancel</Button
				>
				<Button size="sm" onclick={save} disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						{edit ? 'Saving...' : 'Creating...'}
					{:else}
						{edit ? 'Save' : 'Create'}
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
