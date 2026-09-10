/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';
import { NetworkObjectSchema, type NetworkObject } from './object';
import type { Row } from '../components/tree-table';

const nullableString = z
	.string()
	.nullish()
	.transform((value) => value ?? '');

export const VLANPortPolicySchema = z.object({
	mode: z.enum(['', 'access', 'trunk']),
	untaggedVlan: z.number().int().min(1).max(4094).optional(),
	taggedVlans: z.array(z.number().int().min(1).max(4094)).default([])
});

export const NetworkPortSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	switchId: z.number().int().positive(),
	vlanPolicy: VLANPortPolicySchema
});

export const StandardSwitchRCConflictSchema = z.object({
	code: z.enum([
		'standard_switch_member_rc_l3_conflict',
		'standard_switch_member_bridge_ownership_conflict',
		'standard_switch_member_rc_inspection_unavailable',
		'standard_switch_runtime_bridge_inspection_unavailable'
	]),
	port: z.string().optional(),
	member: z.string().optional(),
	conflictingBridge: z.string().optional(),
	dhcp: z.boolean().optional(),
	slaac: z.boolean().optional(),
	staticIPv4: z.boolean().optional(),
	staticIPv6: z.boolean().optional(),
	aliasesIPv4: z.boolean().optional(),
	aliasesIPv6: z.boolean().optional(),
	rcConfigured: z.boolean().optional(),
	runtimeAttached: z.boolean().optional()
});

export const StandardSwitchRCConflictsSchema = z.array(StandardSwitchRCConflictSchema);

export const StandardSwitchSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	bridgeName: z.string(),
	mtu: z.number().int(),
	vlan: z.number().int(),
	private: z.boolean(),
	address: z.string(),
	address6: z.string(),
	addressObj: NetworkObjectSchema.nullable(),
	address6Obj: NetworkObjectSchema.nullable(),
	networkObj: NetworkObjectSchema.nullable(),
	network6Obj: NetworkObjectSchema.nullable(),
	gatewayAddressObj: NetworkObjectSchema.nullable(),
	gateway6AddressObj: NetworkObjectSchema.nullable(),
	networkManual: nullableString,
	network6Manual: nullableString,
	gatewayManual: nullableString,
	gateway6Manual: nullableString,
	ports: z.array(NetworkPortSchema),
	bridgeMacMode: z.enum(['port', 'object']),
	bridgeMacSourcePort: nullableString,
	bridgeMacObjectId: z.number().int().positive().nullable(),
	bridgeMacObject: NetworkObjectSchema.nullable(),
	dhcp: z.boolean(),
	slaac: z.boolean(),
	disableIPv6: z.boolean(),
	defaultRoute: z.boolean(),
	defaultRoute6: z.boolean().default(false),
	disableBridgeOffloads: z.boolean(),
	vlanFiltering: z.boolean().default(false),
	defaultAccessVlan: z.number().int().min(1).max(4094).nullable().default(null),
	hostVlan: z.number().int().min(1).max(4094).nullable().default(null)
});

export const ManualSwitchSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	bridge: z.string(),
	vlanFiltering: z.boolean().default(false),
	defaultAccessVlan: z.number().int().min(1).max(4094).nullable().default(null),
	vlanStateAvailable: z.boolean(),
	vlanStateError: z.string().default(''),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const SwitchListSchema = z.object({
	standard: z.array(StandardSwitchSchema),
	manual: z.array(ManualSwitchSchema)
});

export type StandardSwitchRCConflict = z.infer<typeof StandardSwitchRCConflictSchema>;
export type VLANPortPolicy = z.infer<typeof VLANPortPolicySchema>;
export type StandardSwitch = z.infer<typeof StandardSwitchSchema>;
export type ManualSwitch = z.infer<typeof ManualSwitchSchema>;
export type NetworkSwitch = StandardSwitch | ManualSwitch;
export type SwitchList = z.infer<typeof SwitchListSchema>;

export function isSwitchRuntimeAvailable(networkSwitch: NetworkSwitch): boolean {
	return !('vlanStateAvailable' in networkSwitch) || networkSwitch.vlanStateAvailable;
}

export function isSwitchVMCompatible(networkSwitch: NetworkSwitch): boolean {
	return (
		isSwitchRuntimeAvailable(networkSwitch) &&
		(!networkSwitch.vlanFiltering || networkSwitch.defaultAccessVlan !== null)
	);
}

export interface SwitchRow extends Row {
	id: number;
	name: string;
	mtu: number;
	vlan: number | '-';
	ports: Array<{ name: string; vlanPolicy: VLANPortPolicy }>;
	portsOnly: string[];
	bridgeMacMode: 'port' | 'object';
	bridgeMacSourcePort: string;
	bridgeMacObjectId: number | null;
	bridgeMacObject?: NetworkObject | null;
	networkObj?: NetworkObject;
	networkManual?: string;
	network6Obj?: NetworkObject;
	network6Manual?: string;
	gatewayAddressObj?: NetworkObject;
	gatewayManual?: string;
	gateway6AddressObj?: NetworkObject;
	gateway6Manual?: string;
	disableIPv6: boolean;
	private: boolean;
	dhcp: boolean;
	slaac: boolean;
	defaultRoute: boolean;
	defaultRoute6: boolean;
	disableBridgeOffloads: boolean;
	vlanFiltering: boolean;
	defaultAccessVlan: number | null;
	hostVlan: number | null;
	children?: SwitchRow[];
}

export interface ManualSwitchRow extends Row {
	id: number;
	name: string;
	bridge: string;
	vlanFiltering: boolean;
	defaultAccessVlan: number | null;
	vlanStateAvailable: boolean;
	vlanStateError: string;
	children?: ManualSwitchRow[];
}

export function emptySwitchList(): SwitchList {
	return { standard: [], manual: [] };
}

export function isSwitchList(value: unknown): value is SwitchList {
	return SwitchListSchema.safeParse(value).success;
}
