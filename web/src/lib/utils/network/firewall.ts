import { isValidIPv4, isValidIPv6, isValidPortNumber } from '$lib/utils/string';

type RuleFamily = 'any' | 'inet' | 'inet6' | string;
type RuleProtocol = 'any' | 'tcp' | 'udp' | 'icmp' | string;
type RuleDirection = 'in' | 'out' | string;
type NATType = 'snat' | 'dnat' | 'binat' | string;
type TranslateMode = 'interface' | 'address' | string;

export interface FirewallTrafficRulePayload {
	name: string;
	direction: RuleDirection;
	protocol: RuleProtocol;
	family: RuleFamily;
	ingressInterfaces?: string[];
	egressInterfaces?: string[];
	sourceRaw?: string;
	sourceObjId?: number | null;
	destRaw?: string;
	destObjId?: number | null;
	srcPortsRaw?: string;
	srcPortObjId?: number | null;
	dstPortsRaw?: string;
	dstPortObjId?: number | null;
}

export interface FirewallNATRulePayload {
	name: string;
	natType: NATType;
	policyRoutingEnabled?: boolean | null;
	policyRouteGateway?: string;
	protocol: RuleProtocol;
	family: RuleFamily;
	ingressInterfaces?: string[];
	egressInterfaces?: string[];
	sourceRaw?: string;
	sourceObjId?: number | null;
	destRaw?: string;
	destObjId?: number | null;
	translateMode?: TranslateMode;
	translateToRaw?: string;
	translateToObjId?: number | null;
	dnatTargetRaw?: string;
	dnatTargetObjId?: number | null;
	dstPortsRaw?: string;
	dstPortObjId?: number | null;
	redirectPortsRaw?: string;
	redirectPortObjId?: number | null;
}

export interface RuleValidationResult {
	valid: boolean;
	error?: string;
}

function parseFamily(family: RuleFamily): { value: 'any' | 'inet' | 'inet6' | null; error?: string } {
	const normalized = String(family ?? '').trim().toLowerCase();
	if (normalized === 'any' || normalized === 'inet' || normalized === 'inet6') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported address family: ${String(family ?? '')}` };
}

function parseProtocol(
	protocol: RuleProtocol
): { value: 'any' | 'tcp' | 'udp' | 'icmp' | null; error?: string } {
	const normalized = String(protocol ?? '').trim().toLowerCase();
	if (
		normalized === 'any' ||
		normalized === 'tcp' ||
		normalized === 'udp' ||
		normalized === 'icmp'
	) {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported protocol: ${String(protocol ?? '')}` };
}

function parseDirection(
	direction: RuleDirection
): { value: 'in' | 'out' | null; error?: string } {
	const normalized = String(direction ?? '').trim().toLowerCase();
	if (normalized === 'in' || normalized === 'out') {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported direction: ${String(direction ?? '')}` };
}

function parseNATType(natType: NATType): { value: 'snat' | 'dnat' | 'binat' | null; error?: string } {
	const normalized = String(natType ?? '').trim().toLowerCase();
	if (
		normalized === 'snat' ||
		normalized === 'dnat' ||
		normalized === 'binat'
	) {
		return { value: normalized };
	}
	return { value: null, error: `Unsupported NAT type: ${String(natType ?? '')}` };
}

function parseTranslateMode(
	mode: TranslateMode
): { value: 'interface' | 'address' | null; error?: string } {
	const normalized = String(mode ?? '').trim().toLowerCase();
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

function hasPortSelector(raw: string | undefined, objId: number | null | undefined): boolean {
	return hasSelector(raw, objId);
}

function validateFamilyAgainstRawAddress(
	value: string | undefined,
	family: 'any' | 'inet' | 'inet6',
	allowCIDR: boolean,
	allowAnyLiteral: boolean,
	fieldLabel: string
): string | null {
	const v = String(value ?? '').trim();
	if (!v) return null;

	if (allowAnyLiteral && v.toLowerCase() === 'any') return null;

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

function validateInterfaceList(
	values: string[] | undefined,
	fieldLabel: string
): string | null {
	if (!values) return null;
	if (!Array.isArray(values)) return `${fieldLabel} must be an array`;
	for (const value of values) {
		if (String(value ?? '').trim() === '') {
			return `${fieldLabel} contains an empty interface value`;
		}
	}
	return null;
}

export function validateFirewallTrafficRulePayload(
	payload: FirewallTrafficRulePayload
): RuleValidationResult {
	if (!String(payload.name ?? '').trim()) {
		return { valid: false, error: 'Rule name is required' };
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

	const ingressError = validateInterfaceList(payload.ingressInterfaces, 'Ingress interfaces');
	if (ingressError) return { valid: false, error: ingressError };
	const egressError = validateInterfaceList(payload.egressInterfaces, 'Egress interfaces');
	if (egressError) return { valid: false, error: egressError };
	const ingressInterfaces = (payload.ingressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	const egressInterfaces = (payload.egressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	if (direction === 'in' && egressInterfaces.length > 0) {
		return { valid: false, error: 'Inbound rules do not use egress interfaces' };
	}
	if (direction === 'out' && ingressInterfaces.length > 0) {
		return { valid: false, error: 'Outbound rules do not use ingress interfaces' };
	}

	const sourceError = validateFamilyAgainstRawAddress(payload.sourceRaw, family, true, true, 'Source');
	if (sourceError) return { valid: false, error: sourceError };

	const destError = validateFamilyAgainstRawAddress(payload.destRaw, family, true, true, 'Destination');
	if (destError) return { valid: false, error: destError };

	if (protocol !== 'tcp' && protocol !== 'udp') {
		if (
			hasPortSelector(payload.srcPortsRaw, payload.srcPortObjId) ||
			hasPortSelector(payload.dstPortsRaw, payload.dstPortObjId)
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

export function validateFirewallNATRulePayload(payload: FirewallNATRulePayload): RuleValidationResult {
	if (!String(payload.name ?? '').trim()) {
		return { valid: false, error: 'Rule name is required' };
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
	const translateMode = translateModeResult.value;
	const policyRoutingEnabled = Boolean(payload.policyRoutingEnabled);
	const policyRouteGateway = String(payload.policyRouteGateway ?? '').trim();

	const ingressError = validateInterfaceList(payload.ingressInterfaces, 'Ingress interfaces');
	if (ingressError) return { valid: false, error: ingressError };
	const egressError = validateInterfaceList(payload.egressInterfaces, 'Egress interfaces');
	if (egressError) return { valid: false, error: egressError };

	const ingressInterfaces = (payload.ingressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);
	const egressInterfaces = (payload.egressInterfaces ?? []).map((x) => x.trim()).filter(Boolean);

	const sourceError = validateFamilyAgainstRawAddress(payload.sourceRaw, family, true, true, 'Source');
	if (sourceError) return { valid: false, error: sourceError };
	const destError = validateFamilyAgainstRawAddress(payload.destRaw, family, true, true, 'Destination');
	if (destError) return { valid: false, error: destError };
	const translateError = validateFamilyAgainstRawAddress(
		payload.translateToRaw,
		family,
		false,
		false,
		'Translate target'
	);
	if (translateError) return { valid: false, error: translateError };
	const dnatTargetError = validateFamilyAgainstRawAddress(
		payload.dnatTargetRaw,
		family,
		false,
		false,
		'DNAT target'
	);
	if (dnatTargetError) return { valid: false, error: dnatTargetError };

	const dstPortRawError = validateRawPortSelector(payload.dstPortsRaw, 'Destination ports');
	if (dstPortRawError) return { valid: false, error: dstPortRawError };
	const redirectPortRawError = validateRawPortSelector(payload.redirectPortsRaw, 'Redirect ports');
	if (redirectPortRawError) return { valid: false, error: redirectPortRawError };

	const hasDNATMatchPort = hasPortSelector(payload.dstPortsRaw, payload.dstPortObjId);
	const hasDNATRewritePort = hasPortSelector(payload.redirectPortsRaw, payload.redirectPortObjId);
	if ((hasDNATMatchPort || hasDNATRewritePort) && protocol !== 'tcp' && protocol !== 'udp') {
		return { valid: false, error: 'DNAT port match/rewrite requires TCP or UDP protocol' };
	}
	if (hasDNATRewritePort && !hasDNATMatchPort) {
		return { valid: false, error: 'Redirect port requires destination port match' };
	}

	if (natType === 'snat' || natType === 'binat') {
		if (egressInterfaces.length === 0) {
			return { valid: false, error: `${natType.toUpperCase()} requires at least one egress interface` };
		}
		if (ingressInterfaces.length > 0 && !policyRoutingEnabled) {
			return {
				valid: false,
				error: `${natType.toUpperCase()} ingress interfaces are only used when policy routing is enabled`
			};
		}
		if (
			hasSelector(payload.dnatTargetRaw, payload.dnatTargetObjId) ||
			hasPortSelector(payload.dstPortsRaw, payload.dstPortObjId) ||
			hasPortSelector(payload.redirectPortsRaw, payload.redirectPortObjId)
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
			if (String(payload.translateToRaw ?? '').trim() !== '' && (payload.translateToObjId ?? 0) > 0) {
				return {
					valid: false,
					error: 'Choose either translate target raw value or object, not both'
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
				'Policy route gateway'
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
		if (String(payload.dnatTargetRaw ?? '').trim() !== '' && (payload.dnatTargetObjId ?? 0) > 0) {
			return {
				valid: false,
				error: 'Choose either DNAT target raw value or object, not both'
			};
		}
	}

	return { valid: true };
}
