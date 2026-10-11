import { z } from 'zod/v4';

export const DirectoryPrincipalSchema = z.object({
	sid: z.string(),
	name: z.string(),
	kind: z.enum(['user', 'group']),
	unixId: z.number()
});
export type DirectoryPrincipal = z.infer<typeof DirectoryPrincipalSchema>;

export const KerberosRealmSchema = z.object({
	id: z.number(),
	realm: z.string(),
	kdc: z.array(z.string()).default([]),
	adminServer: z.array(z.string()).default([]),
	kpasswdServer: z.array(z.string()).default([])
});
export type KerberosRealm = z.infer<typeof KerberosRealmSchema>;
export type KerberosRealmInput = Omit<KerberosRealm, 'id'>;
export const KerberosRealmInputSchema = KerberosRealmSchema.omit({ id: true });

export const KerberosKeytabSchema = z.object({
	id: z.number(),
	name: z.string(),
	principals: z.array(z.string()),
	managed: z.boolean(),
	updatedAt: z.string()
});
export type KerberosKeytab = z.infer<typeof KerberosKeytabSchema>;

export const KerberosSettingsSchema = z.object({
	appDefaults: z.string(),
	libDefaults: z.string()
});
export type KerberosSettings = z.infer<typeof KerberosSettingsSchema>;

export const IDMapSchema = z.object({
	domain: z.string(),
	backend: z.enum(['rid', 'ad']),
	rangeLow: z.number(),
	rangeHigh: z.number(),
	schema: z.string()
});
export type IDMap = z.infer<typeof IDMapSchema>;

export const ActiveDirectorySchema = z.object({
	enabled: z.boolean(),
	joined: z.boolean(),
	domain: z.string(),
	workgroup: z.string(),
	computerName: z.string(),
	membershipPending: z.enum(['', 'join', 'leave', 'leave-cleanup']).default(''),
	site: z.string(),
	computerOU: z.string(),
	domainController: z.string(),
	realmId: z.number(),
	keytabId: z.number(),
	kerberosPrincipal: z.string(),
	joinUsername: z.string().default(''),
	joinPasswordConfigured: z.boolean().default(false),
	allowTrustedDomains: z.boolean(),
	useDefaultDomain: z.boolean(),
	disableCache: z.boolean(),
	skipDNSRegistration: z.boolean().default(false),
	joinWarning: z.string().default(''),
	idmapBackend: z.enum(['rid', 'ad', 'autorid']),
	rangeLow: z.number(),
	rangeHigh: z.number(),
	idmapSchema: z.string(),
	trustedDomains: z.array(IDMapSchema).default([])
});
export type ActiveDirectory = z.infer<typeof ActiveDirectorySchema>;
export type ActiveDirectoryInput = Omit<
	ActiveDirectory,
	'enabled' | 'joined' | 'membershipPending' | 'joinPasswordConfigured' | 'joinWarning'
> & {
	joinPassword?: string;
	clearJoinPassword?: boolean;
};

export const ADStatusSchema = z.object({
	state: z.enum(['DISABLED', 'NOT_JOINED', 'UNKNOWN', 'JOINING', 'LEAVING', 'HEALTHY', 'FAULTED']),
	joined: z.boolean(),
	enabled: z.boolean(),
	domain: z.string(),
	error: z.string(),
	domainController: z.string().default(''),
	site: z.string().default(''),
	alternateController: z.string().default(''),
	alternateVerified: z.boolean().default(false),
	warning: z.string().default(''),
	recoveryPending: z.boolean().default(false),
	checkedAt: z.string()
});
export type ADStatus = z.infer<typeof ADStatusSchema>;

export const ADDiscoverySchema = z.object({
	domain: z.string(),
	workgroup: z.string(),
	site: z.string(),
	domainController: z.string(),
	controllers: z.array(z.string()),
	warning: z.string()
});
export type ADDiscovery = z.infer<typeof ADDiscoverySchema>;
