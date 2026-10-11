import { z } from 'zod/v4';

const hostnamePattern =
	/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i;
const ipAddress = z.union([z.ipv4(), z.ipv6()]);
const hostName = z
	.string()
	.trim()
	.toLowerCase()
	.min(1, 'Enter a hostname.')
	.max(253, 'Hostnames must be at most 253 characters.')
	.regex(hostnamePattern, 'Use a valid hostname without spaces or underscores.')
	.refine((value) => !ipAddress.safeParse(value).success, 'Enter a hostname, not an IP address.');

const dnsServer = z
	.string()
	.trim()
	.refine((value) => {
		const [address, zone, ...rest] = value.split('%');
		return (
			ipAddress.safeParse(address).success &&
			rest.length === 0 &&
			(zone === undefined || (/^fe[89ab]/i.test(address) && /^[a-zA-Z0-9_.-]{1,32}$/.test(zone)))
		);
	}, 'Enter a valid IPv4 or IPv6 DNS server address.');

export const HostAliasSchema = z.object({
	address: z.string(),
	hostname: z.string(),
	aliases: z.array(z.string())
});

export const NetworkGlobalConfigSchema = z.object({
	revision: z.number().int().nonnegative(),
	dnsMode: z.enum(['os', 'manual']),
	dnsServers: z.array(z.string()),
	searchDomainsOverride: z.boolean(),
	searchDomains: z.array(z.string()),
	hostAliases: z.array(HostAliasSchema),
	hostname: z.string(),
	effectiveDnsServers: z.array(z.string()),
	effectiveSearchDomains: z.array(z.string()),
	ipv4Gateway: z.string().nullable(),
	ipv6Gateway: z.string().nullable(),
	warnings: z.array(z.string())
});

export const NetworkGlobalConfigUpdateSchema = z
	.object({
		expectedRevision: z.number().int().nonnegative(),
		dnsMode: z.enum(['os', 'manual']),
		dnsServers: z.array(dnsServer).max(3, 'Configure at most three DNS servers.'),
		searchDomainsOverride: z.boolean(),
		searchDomains: z
			.array(
				z
					.string()
					.trim()
					.transform((value) => value.replace(/\.$/, ''))
					.pipe(hostName)
			)
			.max(6, 'Configure at most six search domains.'),
		hostAliases: z
			.array(
				z.object({
					address: z
						.string()
						.trim()
						.refine(
							(value) => ipAddress.safeParse(value).success,
							'Enter a valid IPv4 or IPv6 host address.'
						),
					hostname: hostName,
					aliases: z.array(hostName).max(16, 'Configure at most 16 additional names per host.')
				})
			)
			.max(128, 'Configure at most 128 host aliases.')
	})
	.superRefine((config, ctx) => {
		if (config.dnsMode === 'os' && config.dnsServers.length > 0) {
			ctx.addIssue({
				code: 'custom',
				path: ['dnsServers'],
				message: 'OS-managed mode must not include manual DNS servers.'
			});
		}
		if (!config.searchDomainsOverride && config.searchDomains.length > 0) {
			ctx.addIssue({
				code: 'custom',
				path: ['searchDomains'],
				message: 'Enable the search-domain override to configure search domains.'
			});
		}
		if (config.dnsMode === 'manual' && config.dnsServers.length === 0) {
			ctx.addIssue({
				code: 'custom',
				path: ['dnsServers'],
				message: 'Enter at least one DNS server.'
			});
		}
		if (new Set(config.dnsServers).size !== config.dnsServers.length) {
			ctx.addIssue({
				code: 'custom',
				path: ['dnsServers'],
				message: 'DNS servers must be unique.'
			});
		}
		if (new Set(config.searchDomains).size !== config.searchDomains.length) {
			ctx.addIssue({
				code: 'custom',
				path: ['searchDomains'],
				message: 'Search domains must be unique.'
			});
		}
		if (config.searchDomains.join(' ').length > 255) {
			ctx.addIssue({
				code: 'custom',
				path: ['searchDomains'],
				message: 'The search list must be at most 255 characters.'
			});
		}
		const hostNames = new Set<string>();
		config.hostAliases.forEach((host, index) => {
			for (const name of [host.hostname, ...host.aliases]) {
				const key = `${host.address.includes(':') ? 'ipv6' : 'ipv4'}/${name}`;
				if (hostNames.has(key)) {
					ctx.addIssue({
						code: 'custom',
						path: ['hostAliases', index],
						message: 'Each hostname must be unique within an address family.'
					});
				}
				hostNames.add(key);
			}
		});
	});

export type NetworkGlobalConfig = z.infer<typeof NetworkGlobalConfigSchema>;
export type NetworkGlobalConfigUpdate = z.infer<typeof NetworkGlobalConfigUpdateSchema>;
