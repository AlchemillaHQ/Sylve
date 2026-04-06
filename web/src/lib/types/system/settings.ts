import { z } from 'zod/v4';

export type AvailableService =
	| 'virtualization'
	| 'jails'
	| 'dhcp-server'
	| 'samba-server'
	| 'wol-server'
	| 'firewall'
	| 'wireguard';

export const BasicSettingsSchema = z.object({
	pools: z.array(z.string()),
	services: z.array(
		z.enum([
			'virtualization',
			'jails',
			'dhcp-server',
			'samba-server',
			'wol-server',
			'firewall',
			'wireguard'
		])
	),
	initialized: z.boolean()
});

export type BasicSettings = z.infer<typeof BasicSettingsSchema>;
