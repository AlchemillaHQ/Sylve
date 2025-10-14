import { z } from 'zod/v4';
import { ManualSwitchSchema, StandardSwitchSchema } from './switch';
import { NetworkObjectSchema } from './object';

export const DHCPConfigSchema = z.object({
	id: z.number(),
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
