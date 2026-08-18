import { z } from 'zod/v4';
import { ManualSwitchSchema, StandardSwitchSchema } from './switch';
import { NetworkObjectSchema } from './object';
import type { Row } from '../components/tree-table';

export const DHCPConfigSchema = z.object({
	id: z.number().int().positive(),
	standardSwitches: z.array(StandardSwitchSchema),
	manualSwitches: z.array(ManualSwitchSchema),
	dnsServers: z.array(z.string()),
	domain: z.string(),
	expandHosts: z.boolean().default(true),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const DHCPRangeSchema = z.object({
	id: z.number(),
	type: z.enum(['ipv4', 'ipv6']),
	startIp: z.string(),
	endIp: z.string(),
	standardSwitchId: z.number().optional().nullable(),
	standardSwitch: StandardSwitchSchema.nullable(),
	manualSwitchId: z.number().optional().nullable(),
	manualSwitch: ManualSwitchSchema.nullable(),
	expiry: z.number(),
	raOnly: z.boolean().default(false),
	slaac: z.boolean().default(false),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const DHCPStaticLeaseSchema = z.object({
	id: z.number(),
	hostname: z.string(),
	comments: z.string().optional().nullable(),
	expiry: z.number().default(0),

	ipObjectId: z.number().optional().nullable(),
	macObjectId: z.number().optional().nullable(),
	duidObjectId: z.number().optional().nullable(),

	ipObject: NetworkObjectSchema.nullable(),
	macObject: NetworkObjectSchema.nullable(),
	duidObject: NetworkObjectSchema.nullable(),

	dhcpRangeId: z.number(),
	dhcpRange: DHCPRangeSchema.optional().nullable(),

	createdAt: z.string(),
	updatedAt: z.string()
});

export const FileLeaseSchema = z.object({
	expiry: z.number(),
	mac: z.string(),
	ip: z.string(),
	iaid: z.string(),
	hostname: z.string(),
	clientId: z.string(),
	duid: z.string()
});

export const LeasesSchema = z.object({
	file: z.array(FileLeaseSchema).default([]),
	db: z.array(DHCPStaticLeaseSchema).default([])
});

export type DHCPConfig = z.infer<typeof DHCPConfigSchema>;
export type DHCPRange = z.infer<typeof DHCPRangeSchema>;
export type DHCPStaticLease = z.infer<typeof DHCPStaticLeaseSchema>;
export type FileLease = z.infer<typeof FileLeaseSchema>;
export type Leases = z.infer<typeof LeasesSchema>;

export function emptyLeases(): Leases {
	return {
		file: [],
		db: []
	};
}

export function emptyDHCPConfig(): DHCPConfig {
	return {
		id: 0,
		standardSwitches: [],
		manualSwitches: [],
		dnsServers: [],
		domain: '',
		expandHosts: true,
		createdAt: '',
		updatedAt: ''
	};
}

export function isDHCPConfig(value: unknown): value is DHCPConfig {
	return DHCPConfigSchema.safeParse(value).success;
}

interface DHCPLeaseRowBase extends Omit<Row, 'id'> {
	id: string;
	identifier: string;
	hostname: string;
	ip: string;
	range: string;
	switch: string;
	mac: string;
	duid: string;
}

export interface DHCPStaticLeaseRow extends DHCPLeaseRowBase {
	type: 'static';
	dbId: number;
	expiry: 'never';
}

export interface DHCPDynamicLeaseRow extends DHCPLeaseRowBase {
	type: 'dynamic';
	dbId?: never;
	expiry: number | 'never';
}

export type DHCPLeaseRow = DHCPStaticLeaseRow | DHCPDynamicLeaseRow;
