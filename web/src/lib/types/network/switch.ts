import { z } from 'zod/v4';
import { NetworkObjectSchema } from './object';

export const NetworkPortSchema = z.object({
	id: z.number(),
	name: z.string(),
	switchId: z.number()
});

export const StandardSwitchSchema = z.object({
	id: z.number(),
	name: z.string(),
	bridgeName: z.string(),
	mtu: z.number(),
	vlan: z.number(),
	private: z.boolean(),
	address: z.string(),
	address6: z.string(),
	addressObj: NetworkObjectSchema.nullable(),
	address6Obj: NetworkObjectSchema.nullable(),
	networkObj: NetworkObjectSchema.nullable(),
	network6Obj: NetworkObjectSchema.nullable(),
	gatewayAddressObj: NetworkObjectSchema.nullable(),
	gateway6AddressObj: NetworkObjectSchema.nullable(),
	ports: z.array(NetworkPortSchema).optional().nullable(),
	dhcp: z.boolean().optional(),
	slaac: z.boolean(),
	disableIPv6: z.boolean(),
	defaultRoute: z.boolean()
});

export const ManualSwitchSchema = z.object({
	id: z.number(),
	name: z.string(),
	bridge: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const SwitchListSchema = z.object({
	standard: z.array(StandardSwitchSchema).optional(),
	manual: z.array(ManualSwitchSchema).optional()
});

export type StandardSwitch = z.infer<typeof StandardSwitchSchema>;
export type ManualSwitch = z.infer<typeof ManualSwitchSchema>;
export type SwitchList = z.infer<typeof SwitchListSchema>;
