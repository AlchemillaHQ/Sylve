import { z } from 'zod/v4';
import { ManualSwitchSchema, StandardSwitchSchema } from './switch';

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

export type DHCPConfig = z.infer<typeof DHCPConfigSchema>;
export type DHCPRange = z.infer<typeof DHCPRangeSchema>;
