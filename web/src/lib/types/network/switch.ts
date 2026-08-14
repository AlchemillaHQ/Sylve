import { z } from 'zod/v4';
import { NetworkObjectSchema } from './object';

const nullableString = z
	.string()
	.nullish()
	.transform((value) => value ?? '');

export const NetworkPortSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	switchId: z.number().int().positive()
});

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
	dhcp: z.boolean(),
	slaac: z.boolean(),
	disableIPv6: z.boolean(),
	defaultRoute: z.boolean(),
	disableBridgeOffloads: z.boolean()
});

export const ManualSwitchSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	bridge: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const SwitchListSchema = z.object({
	standard: z.array(StandardSwitchSchema),
	manual: z.array(ManualSwitchSchema)
});

export type StandardSwitch = z.infer<typeof StandardSwitchSchema>;
export type ManualSwitch = z.infer<typeof ManualSwitchSchema>;
export type SwitchList = z.infer<typeof SwitchListSchema>;

export function emptySwitchList(): SwitchList {
	return { standard: [], manual: [] };
}

export function isSwitchList(value: unknown): value is SwitchList {
	return SwitchListSchema.safeParse(value).success;
}
