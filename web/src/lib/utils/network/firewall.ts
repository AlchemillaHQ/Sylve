import type {
	FirewallTrafficRuleUpsertRequest,
	FirewallNATRuleUpsertRequest
} from '$lib/api/network/firewall';
import { isValidIPv4, isValidIPv6, isValidPortNumber } from '$lib/utils/string';

export interface RuleValidationResult {
	valid: boolean;
	error?: string;
}

export function firewallValidationDetail(data: unknown): string | null {
	if (
		typeof data === 'object' &&
		data !== null &&
		'detail' in data &&
		typeof data.detail === 'string'
	) {
		return data.detail;
	}
	return null;
}

function parseFamily(family: string | undefined): {
	value: 'any' | 'inet' | 'inet6' | null;
	error?: string;
} {
	const normalized = String(family ?? '')
		.trim()
		.toLowerCase();
	if (normalized === 'any' || normalized === 'inet' || normalized === 'inet6') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported address family: ${String(family ?? '')}` };
}

function parseProtocol(protocol: string | undefined): {
	value: 'any' | 'tcp' | 'udp' | 'tcp_udp' | 'icmp' | 'icmp6' | null;
	error?: string;
} {
	const normalized = String(protocol ?? '')
		.trim()
		.toLowerCase();
	if (
		normalized === 'any' ||
		normalized === 'tcp' ||
		normalized === 'udp' ||
		normalized === 'tcp_udp' ||
		normalized === 'icmp' ||
		normalized === 'icmp6'
	) {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported protocol: ${String(protocol ?? '')}` };
}

function parseDirection(direction: string | undefined): {
	value: 'in' | 'out' | null;
	error?: string;
} {
	const normalized = String(direction ?? '')
		.trim()
		.toLowerCase();
	if (normalized === 'in' || normalized === 'out') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported direction: ${String(direction ?? '')}` };
}

function parseNATType(natType: string): {
	value: 'snat' | 'dnat' | 'binat' | null;
	error?: string;
} {
	const normalized = String(natType ?? '')
		.trim()
		.toLowerCase();
	if (normalized === 'snat' || normalized === 'dnat' || normalized === 'binat') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported NAT type: ${String(natType ?? '')}` };
}

function parseTranslateMode(mode: string | undefined): {
	value: 'interface' | 'address' | null;
	error?: string;
} {
	const normalized = String(mode ?? '')
		.trim()
		.toLowerCase();
	if (normalized === '') {
		return { value: 'interface' };
	}
	if (normalized === 'interface' || normalized === 'address') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported translate mode: ${String(mode ?? '')}` };
}

function hasSelector(raw: string | undefined, objId: number | null | undefined): boolean {
	return String(raw ?? '').trim() !== '' || (objId ?? 0) > 0;
}

function validateFamilyAgainstRawAddress(
	value: string | undefined,
	family: 'any' | 'inet' | 'inet6',
	allowCIDR: boolean,
	allowAnyLiteral: boolean,
	fieldLabel: string,
	allowDynamic = true
): string | null {
	const v = String(value ?? '').trim();
	if (!v) return null;

	if (allowAnyLiteral && v.toLowerCase() === 'any') return null;
	if (allowDynamic && /^\([A-Za-z0-9][A-Za-z0-9_.-]{0,63}\)$/.test(v)) return null;

	const isV4 = isValidIPv4(v, false);
	const isV6 = isValidIPv6(v, false);
	const isV4CIDR = isValidIPv4(v, true);
	const isV6CIDR = isValidIPv6(v, true);

	if (!(isV4 || isV6 || isV4CIDR || isV6CIDR)) {
		return `${fieldLabel} is not a valid IP/CIDR value`;
	}

	if (!allowCIDR && (isV4CIDR || isV6CIDR)) {
		return `${fieldLabel} must be a host IP (CIDR not allowed)`;
	}

	if (family === 'inet' && (isV6 || isV6CIDR)) {
		return `${fieldLabel} must be IPv4 for family inet`;
	}
	if (family === 'inet6' && (isV4 || isV4CIDR)) {
		return `${fieldLabel} must be IPv6 for family inet6`;
	}

	return null;
}

function parsePortToken(value: string): boolean {
	const v = value.trim();
	if (!v) return false;

	if (v.includes(':')) {
		if (!/^\d+:\d+$/.test(v)) return false;
		const parts = v.split(':');
		if (parts.length !== 2) return false;
		const start = Number.parseInt(parts[0], 10);
		const end = Number.parseInt(parts[1], 10);
		if (!isValidPortNumber(start) || !isValidPortNumber(end)) return false;
		return start <= end;
	}

	if (!/^\d+$/.test(v)) return false;
	return isValidPortNumber(v);
}

function validateRawPortSelector(raw: string | undefined, fieldLabel: string): string | null {
	let value = String(raw ?? '').trim();
	if (!value) return null;

	if (value.startsWith('{') && value.endsWith('}')) {
		value = value.slice(1, -1).trim();
	}

	const tokens = value.split(',');
	for (const token of tokens) {
		const part = token.trim();
		if (!part) return `${fieldLabel} contains an empty token`;
		if (!parsePortToken(part)) {
			return `${fieldLabel} has invalid port token: ${part}`;
		}
	}

	return null;
}

function validateInterfaceList(values: string[] | undefined, fieldLabel: string): string | null {
	if (!values) return null;
	if (!Array.isArray(values)) return `${fieldLabel} must be an array`;
	for (const value of values) {
		if (String(value ?? '').trim() === '') {
			return `${fieldLabel} contains an empty interface value`;
		}
	}
	return null;
}

function validateFirewallInterfaceList(
	values: string[] | undefined,
	fieldLabel: string
): string | null {
	const basicError = validateInterfaceList(values, fieldLabel);
	if (basicError) return basicError;
	if (!values) return null;
	if (values.length > 64) return `${fieldLabel} cannot contain more than 64 entries`;

	const seen = new Set<string>();
	for (const value of values) {
		const name = String(value ?? '').trim();
		if (name.length > 64 || !/^[A-Za-z0-9][A-Za-z0-9_.:-]*$/.test(name)) {
			return `${fieldLabel} contains an invalid interface name`;
		}
		if (seen.has(name)) return `${fieldLabel} contains a duplicate interface`;
		seen.add(name);
	}

	return null;
}

function validateFirewallSelectorPair(
	raw: string | undefined,
	objectId: number | null | undefined,
	fieldLabel: string
): string | null {
	if (String(raw ?? '').length > 2048) return `${fieldLabel} is too long`;
	if (objectId !== null && objectId !== undefined) {
		if (!Number.isInteger(objectId) || objectId <= 0) return `${fieldLabel} object is invalid`;
		if (String(raw ?? '').trim() !== '') {
			return `${fieldLabel} cannot use both a raw value and an object`;
		}
	}
	return null;
}

export function validateFirewallTrafficRulePayload(
	payload: FirewallTrafficRuleUpsertRequest
): RuleValidationResult {
	const name = String(payload.name ?? '').trim();
	if (!name) {
		return { valid: false, error: 'Rule name is required' };
	}
	if (name.length > 128) return { valid: false, error: 'Rule name cannot exceed 128 characters' };
	if (String(payload.description ?? '').trim().length > 2048) {
		return { valid: false, error: 'Rule description cannot exceed 2048 characters' };
	}
	if (
		payload.priority !== undefined &&
		(!Number.isInteger(payload.priority) || payload.priority <= 0 || payload.priority > 1_000_000)
	) {
		return { valid: false, error: 'Priority must be a positive whole number' };
	}
	if (payload.kind === 'advanced') {
		const raw = payload.rawPF ?? '';
		if (!raw.trim()) return { valid: false, error: 'Advanced PF filtering rules are required' };
		if (raw.length > 262144 || raw.includes('\0') || raw.includes('\r')) {
			return { valid: false, error: 'Advanced PF text is invalid or too long' };
		}
		return { valid: true };
	}
	if (payload.rawPF) return { valid: false, error: 'PF text requires an Advanced PF row' };
	const state = payload.statePolicy ?? 'default';
	const response = payload.blockResponse ?? 'default';
	if (
		!['default', 'keep', 'none'].includes(state) ||
		!['default', 'drop', 'return'].includes(response)
	) {
		return { valid: false, error: 'Unsupported state policy or block response' };
	}
	if (payload.action !== 'pass' && state !== 'default') {
		return { valid: false, error: 'State handling is only available for Pass rules' };
	}
	if (payload.action !== 'block' && response !== 'default') {
		return { valid: false, error: 'Block response is only available for Block rules' };
	}
	if (payload.action !== 'pass' && payload.action !== 'block') {
		return { valid: false, error: 'Unsupported firewall action' };
	}

	const familyResult = parseFamily(payload.family);
	if (!familyResult.value) return { valid: false, error: familyResult.error };
	const protocolResult = parseProtocol(payload.protocol);
	if (!protocolResult.value) return { valid: false, error: protocolResult.error };
	const directionResult = parseDirection(payload.direction);
	if (!directionResult.value) return { valid: false, error: directionResult.error };
	const family = familyResult.value;
	const protocol = protocolResult.value;
	const direction = directionResult.value;
	const addressFamily = protocol === 'icmp' ? 'inet' : protocol === 'icmp6' ? 'inet6' : family;
	if ((protocol === 'icmp' && family === 'inet6') || (protocol === 'icmp6' && family === 'inet')) {
		return { valid: false, error: 'ICMP protocol does not match the selected address family' };
	}
	const types = payload.icmpTypes ?? [];
	const allowed = protocol === 'icmp6' ? icmp6TypeOptions : icmpTypeOptions;
	if (types.length > 0 && protocol !== 'icmp' && protocol !== 'icmp6') {
		return { valid: false, error: 'ICMP types require an ICMP protocol' };
	}
	if (
		new Set(types).size !== types.length ||
		types.some((type) => !allowed.some((option) => option.value === type))
	) {
		return { valid: false, error: 'Invalid ICMP type selection' };
	}

	const ingressError = validateFirewallInterfaceList(
		payload.ingressInterfaces,
		'Ingress interfaces'
	);
	if (ingressError) return { valid: false, error: ingressError };
	const egressError = validateFirewallInterfaceList(payload.egressInterfaces, 'Egress interfaces');
	if (egressError) return { valid: false, error: egressError };
	const ingressInterfaces = (payload.ingressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	const egressInterfaces = (payload.egressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	if (direction === 'in' && egressInterfaces.length > 0) {
		return { valid: false, error: 'Inbound rules do not use egress interfaces' };
	}
	if (direction === 'out' && ingressInterfaces.length > 0) {
		return { valid: false, error: 'Outbound rules do not use ingress interfaces' };
	}

	const selectorPairs = [
		[payload.sourceRaw, payload.sourceObjId, 'Source'],
		[payload.destRaw, payload.destObjId, 'Destination'],
		[payload.srcPortsRaw, payload.srcPortObjId, 'Source ports'],
		[payload.dstPortsRaw, payload.dstPortObjId, 'Destination ports']
	] as const;
	for (const [raw, objectId, label] of selectorPairs) {
		const selectorError = validateFirewallSelectorPair(raw, objectId, label);
		if (selectorError) return { valid: false, error: selectorError };
	}

	const sourceError = validateFamilyAgainstRawAddress(
		payload.sourceRaw,
		addressFamily,
		true,
		true,
		'Source'
	);
	if (sourceError) return { valid: false, error: sourceError };

	const destError = validateFamilyAgainstRawAddress(
		payload.destRaw,
		addressFamily,
		true,
		true,
		'Destination'
	);
	if (destError) return { valid: false, error: destError };

	if (protocol !== 'tcp' && protocol !== 'udp' && protocol !== 'tcp_udp') {
		if (
			hasSelector(payload.srcPortsRaw, payload.srcPortObjId) ||
			hasSelector(payload.dstPortsRaw, payload.dstPortObjId)
		) {
			return { valid: false, error: 'Port selectors are only allowed for TCP/UDP rules' };
		}
	} else {
		const srcPortError = validateRawPortSelector(payload.srcPortsRaw, 'Source ports');
		if (srcPortError) return { valid: false, error: srcPortError };
		const dstPortError = validateRawPortSelector(payload.dstPortsRaw, 'Destination ports');
		if (dstPortError) return { valid: false, error: dstPortError };
	}

	return { valid: true };
}

export function validateFirewallNATRulePayload(
	payload: FirewallNATRuleUpsertRequest
): RuleValidationResult {
	const name = String(payload.name ?? '').trim();
	if (!name) {
		return { valid: false, error: 'Rule name is required' };
	}
	if (name.length > 128) return { valid: false, error: 'Rule name cannot exceed 128 characters' };
	if (String(payload.description ?? '').trim().length > 2048) {
		return { valid: false, error: 'Rule description cannot exceed 2048 characters' };
	}
	if (
		payload.priority !== undefined &&
		(!Number.isInteger(payload.priority) || payload.priority <= 0 || payload.priority > 1_000_000)
	) {
		return { valid: false, error: 'Priority must be a positive whole number' };
	}

	const natTypeResult = parseNATType(payload.natType);
	if (!natTypeResult.value) return { valid: false, error: natTypeResult.error };
	const protocolResult = parseProtocol(payload.protocol);
	if (!protocolResult.value) return { valid: false, error: protocolResult.error };
	const familyResult = parseFamily(payload.family);
	if (!familyResult.value) return { valid: false, error: familyResult.error };
	const translateModeResult = parseTranslateMode(payload.translateMode);
	if (!translateModeResult.value) return { valid: false, error: translateModeResult.error };

	const natType = natTypeResult.value;
	const protocol = protocolResult.value;
	const family = familyResult.value;
	const addressFamily = protocol === 'icmp' ? 'inet' : protocol === 'icmp6' ? 'inet6' : family;
	const translateMode = translateModeResult.value;
	const policyRoutingEnabled = Boolean(payload.policyRoutingEnabled);
	if (natType !== 'dnat' && payload.passRedirectedTraffic) {
		return { valid: false, error: 'Pass Redirected Traffic is only available for DNAT' };
	}
	if (
		!['all', 'private', 'public'].includes(payload.targetAddressScope ?? 'all') ||
		!['single', 'round_robin'].includes(payload.targetHandling ?? 'single')
	) {
		return { valid: false, error: 'Unsupported NAT target settings' };
	}
	if (natType === 'binat' && payload.targetHandling === 'round_robin') {
		return { valid: false, error: 'BINAT requires a single translation target' };
	}
	if ((protocol === 'icmp' && family === 'inet6') || (protocol === 'icmp6' && family === 'inet')) {
		return { valid: false, error: 'ICMP protocol does not match the selected address family' };
	}
	const policyRouteGateway = String(payload.policyRouteGateway ?? '').trim();
	if (policyRouteGateway.length > 64) {
		return { valid: false, error: 'Policy route gateway cannot exceed 64 characters' };
	}

	const ingressError = validateFirewallInterfaceList(
		payload.ingressInterfaces,
		'Ingress interfaces'
	);
	if (ingressError) return { valid: false, error: ingressError };
	const egressError = validateFirewallInterfaceList(payload.egressInterfaces, 'Egress interfaces');
	if (egressError) return { valid: false, error: egressError };

	const ingressInterfaces = (payload.ingressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	const egressInterfaces = (payload.egressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);

	const selectorPairs = [
		[payload.sourceRaw, payload.sourceObjId, 'Source'],
		[payload.destRaw, payload.destObjId, 'Destination'],
		[payload.translateToRaw, payload.translateToObjId, 'Translate target'],
		[payload.dnatTargetRaw, payload.dnatTargetObjId, 'DNAT target'],
		[payload.dstPortsRaw, payload.dstPortObjId, 'Destination ports'],
		[payload.redirectPortsRaw, payload.redirectPortObjId, 'Redirect ports']
	] as const;
	for (const [raw, objectId, label] of selectorPairs) {
		const selectorError = validateFirewallSelectorPair(raw, objectId, label);
		if (selectorError) return { valid: false, error: selectorError };
	}

	const sourceError = validateFamilyAgainstRawAddress(
		payload.sourceRaw,
		addressFamily,
		true,
		true,
		'Source'
	);
	if (sourceError) return { valid: false, error: sourceError };
	const destError = validateFamilyAgainstRawAddress(
		payload.destRaw,
		addressFamily,
		true,
		true,
		'Destination'
	);
	if (destError) return { valid: false, error: destError };
	const translateError = validateFamilyAgainstRawAddress(
		payload.translateToRaw,
		addressFamily,
		false,
		false,
		'Translate target'
	);
	if (translateError) return { valid: false, error: translateError };
	const dnatTargetError = validateFamilyAgainstRawAddress(
		payload.dnatTargetRaw,
		addressFamily,
		false,
		false,
		'DNAT target'
	);
	if (dnatTargetError) return { valid: false, error: dnatTargetError };

	const dstPortRawError = validateRawPortSelector(payload.dstPortsRaw, 'Destination ports');
	if (dstPortRawError) return { valid: false, error: dstPortRawError };
	const redirectPortRawError = validateRawPortSelector(payload.redirectPortsRaw, 'Redirect ports');
	if (redirectPortRawError) return { valid: false, error: redirectPortRawError };

	const hasDNATMatchPort = hasSelector(payload.dstPortsRaw, payload.dstPortObjId);
	const hasDNATRewritePort = hasSelector(payload.redirectPortsRaw, payload.redirectPortObjId);
	if (
		(hasDNATMatchPort || hasDNATRewritePort) &&
		protocol !== 'tcp' &&
		protocol !== 'udp' &&
		protocol !== 'tcp_udp'
	) {
		return { valid: false, error: 'DNAT port match/rewrite requires TCP or UDP protocol' };
	}
	if (hasDNATRewritePort && !hasDNATMatchPort) {
		return { valid: false, error: 'Redirect port requires destination port match' };
	}

	if (natType === 'snat' || natType === 'binat') {
		if (egressInterfaces.length === 0) {
			return {
				valid: false,
				error: `${natType.toUpperCase()} requires at least one egress interface`
			};
		}
		if (ingressInterfaces.length > 0 && !policyRoutingEnabled) {
			return {
				valid: false,
				error: `${natType.toUpperCase()} ingress interfaces are only used when policy routing is enabled`
			};
		}
		if (
			hasSelector(payload.dnatTargetRaw, payload.dnatTargetObjId) ||
			hasDNATMatchPort ||
			hasDNATRewritePort
		) {
			return { valid: false, error: `${natType.toUpperCase()} does not allow DNAT-only fields` };
		}

		if (translateMode === 'interface') {
			if (hasSelector(payload.translateToRaw, payload.translateToObjId)) {
				return {
					valid: false,
					error: 'Translate target is not allowed when Translate Mode is Interface Address'
				};
			}
		} else {
			if (!hasSelector(payload.translateToRaw, payload.translateToObjId)) {
				return {
					valid: false,
					error: 'Translate target is required when Translate Mode is Specific Address'
				};
			}
		}

		if (policyRoutingEnabled) {
			if (egressInterfaces.length !== 1) {
				return {
					valid: false,
					error: 'Policy routing requires exactly one egress interface'
				};
			}
			if (!policyRouteGateway) {
				return {
					valid: false,
					error: 'Policy route gateway is required when policy routing is enabled'
				};
			}
			if (family === 'any') {
				return {
					valid: false,
					error: 'Policy route gateway requires family IPv4 or IPv6'
				};
			}
			const gatewayError = validateFamilyAgainstRawAddress(
				policyRouteGateway,
				family,
				false,
				false,
				'Policy route gateway',
				false
			);
			if (gatewayError) return { valid: false, error: gatewayError };
		}
	}

	if (natType === 'dnat') {
		if (policyRoutingEnabled) {
			return { valid: false, error: 'DNAT does not allow policy routing' };
		}
		if (ingressInterfaces.length === 0) {
			return { valid: false, error: 'DNAT requires at least one ingress interface' };
		}
		if (egressInterfaces.length > 0) {
			return { valid: false, error: 'DNAT does not use egress interfaces' };
		}
		if (String(payload.translateMode ?? '').trim() !== '') {
			return { valid: false, error: 'DNAT cannot use Translate Mode' };
		}
		if (hasSelector(payload.translateToRaw, payload.translateToObjId)) {
			return { valid: false, error: 'DNAT cannot use SNAT/BINAT translate target fields' };
		}
		if (!hasSelector(payload.dnatTargetRaw, payload.dnatTargetObjId)) {
			return { valid: false, error: 'DNAT target host is required' };
		}
	}

	return { valid: true };
}

export const icmpTypeOptions = [
	'echoreq',
	'echorep',
	'unreach',
	'squench',
	'redir',
	'althost',
	'routeradv',
	'routersol',
	'timex',
	'paramprob',
	'timereq',
	'timerep',
	'inforeq',
	'inforep',
	'maskreq',
	'maskrep',
	'trace',
	'dataconv',
	'mobredir',
	'ipv6-where',
	'ipv6-here',
	'mobregreq',
	'mobregrep',
	'skip',
	'photuris'
].map((value) => ({ value, label: value }));

export const icmp6TypeOptions = [
	'unreach',
	'toobig',
	'timex',
	'paramprob',
	'echoreq',
	'echorep',
	'groupqry',
	'listqry',
	'grouprep',
	'listenrep',
	'groupterm',
	'listendone',
	'routersol',
	'routeradv',
	'neighbrsol',
	'neighbradv',
	'redir',
	'routrrenum',
	'wrureq',
	'wrurep',
	'fqdnreq',
	'fqdnrep',
	'niqry',
	'nirep',
	'mtraceresp',
	'mtrace',
	'listenrepv2'
].map((value) => ({ value, label: value }));
