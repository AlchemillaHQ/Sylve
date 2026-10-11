import {
	ActiveDirectorySchema,
	KerberosRealmSchema,
	KerberosSettingsSchema,
	type ActiveDirectory,
	type DirectoryPrincipal,
	type KerberosKeytab,
	type KerberosRealm,
	type KerberosSettings
} from '$lib/types/directory-services';

export const demoDirectoryPrincipals: DirectoryPrincipal[] = [
	{
		sid: 'S-1-5-21-123456-234567-345678-1100',
		name: 'EXAMPLE\\alice',
		kind: 'user',
		unixId: 1001100
	},
	{
		sid: 'S-1-5-21-123456-234567-345678-1101',
		name: 'EXAMPLE\\bob',
		kind: 'user',
		unixId: 1001101
	},
	{
		sid: 'S-1-5-21-123456-234567-345678-2100',
		name: 'EXAMPLE\\File Users',
		kind: 'group',
		unixId: 1002100
	}
];

type State = {
	ad: ActiveDirectory;
	realms: KerberosRealm[];
	keytabs: KerberosKeytab[];
	settings: KerberosSettings;
};
type DemoDirectoryResponse<T = unknown> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};
const states = new Map<string, State>();
function stateFor(hostname: string): State {
	let state = states.get(hostname);
	if (!state) {
		state = {
			ad: {
				enabled: true,
				joined: true,
				membershipPending: '',
				domain: 'EXAMPLE.COM',
				workgroup: 'EXAMPLE',
				computerName: hostname.toUpperCase(),
				site: '',
				computerOU: '',
				domainController: '',
				realmId: 1,
				keytabId: 0,
				kerberosPrincipal: '',
				joinUsername: '',
				joinPasswordConfigured: false,
				allowTrustedDomains: false,
				useDefaultDomain: false,
				disableCache: false,
				skipDNSRegistration: false,
				joinWarning: '',
				idmapBackend: 'rid',
				rangeLow: 1000000,
				rangeHigh: 1999999999,
				idmapSchema: 'rfc2307',
				trustedDomains: []
			},
			realms: [{ id: 1, realm: 'EXAMPLE.COM', kdc: [], adminServer: [], kpasswdServer: [] }],
			keytabs: [
				{
					id: 1,
					name: 'AD_MACHINE_ACCOUNT',
					principals: [
						`${hostname.toUpperCase()}$@EXAMPLE.COM`,
						`cifs/${hostname}.example.com@EXAMPLE.COM`
					],
					managed: true,
					updatedAt: new Date().toISOString()
				}
			],
			settings: { appDefaults: '', libDefaults: '' }
		};
		states.set(hostname, state);
	}
	return state;
}

export function handleDemoDirectoryRequest<T = unknown>(
	config: { data?: unknown },
	parsed: URL,
	path: string,
	method: string,
	hostname: string
): DemoDirectoryResponse<T> | null {
	if (!path.startsWith('/directory-services/')) return null;
	const state = stateFor(hostname);
	const body =
		typeof config.data === 'object' && config.data !== null
			? (config.data as Record<string, unknown>)
			: {};
	function respond(data: unknown, error = ''): DemoDirectoryResponse<T> {
		return {
			status: error ? 409 : 200,
			data: {
				status: error ? 'error' : 'success',
				message: 'demo_directory_services',
				error,
				data
			} as T,
			headers: { 'content-type': 'application/json' },
			ok: !error
		};
	}
	if (path === '/directory-services/active-directory') {
		if (method === 'PUT') {
			const joinUsername = body.keytabId
				? ''
				: typeof body.joinUsername === 'string'
					? body.joinUsername.trim()
					: state.ad.joinUsername;
			const replacingPassword = typeof body.joinPassword === 'string' && body.joinPassword !== '';
			if (replacingPassword && (!joinUsername || body.keytabId || body.clearJoinPassword))
				return respond(null, 'Invalid enrollment credentials');
			if (
				joinUsername &&
				!body.keytabId &&
				!replacingPassword &&
				!body.clearJoinPassword &&
				(joinUsername !== state.ad.joinUsername ||
					String(body.domain).trim().toUpperCase() !== state.ad.domain)
			)
				return respond(null, 'Enter a new join password when changing the username or domain');
			const parsed = ActiveDirectorySchema.safeParse({
				...body,
				joinUsername,
				joinPasswordConfigured:
					!!joinUsername &&
					!body.keytabId &&
					!body.clearJoinPassword &&
					(replacingPassword || state.ad.joinPasswordConfigured),
				enabled: state.ad.enabled,
				joined: state.ad.joined,
				joinWarning: state.ad.joinWarning
			});
			if (!parsed.success) return respond(null, 'Invalid AD configuration');
			state.ad = parsed.data;
		}
		return respond(state.ad);
	}
	if (path.endsWith('/active-directory/join')) {
		state.ad.enabled = true;
		state.ad.joined = true;
		state.ad.membershipPending = '';
		state.ad.joinWarning = '';
		if (!state.keytabs.some((keytab) => keytab.managed))
			state.keytabs.push({
				id: Math.max(0, ...state.keytabs.map((keytab) => keytab.id)) + 1,
				name: 'AD_MACHINE_ACCOUNT',
				principals: [`${state.ad.computerName}$@${state.ad.domain}`],
				managed: true,
				updatedAt: new Date().toISOString()
			});
		return respond(null);
	}
	if (path.endsWith('/active-directory/leave')) {
		state.ad.enabled = false;
		state.ad.joined = false;
		state.ad.membershipPending = '';
		state.ad.joinWarning = '';
		state.keytabs = state.keytabs.filter((keytab) => !keytab.managed);
		return respond(null);
	}
	if (path.endsWith('/active-directory/enabled')) {
		if (!state.ad.joined) return respond(null, 'Join the domain first');
		state.ad.enabled = body.enabled === true;
		return respond(null);
	}
	if (path === '/directory-services/recovery') return respond({ pending: false });
	if (path.startsWith('/directory-services/recovery/'))
		return respond(null, 'Recovery bundles are not available in demo mode.');
	if (path.endsWith('/active-directory/discover')) {
		const domain = String(body.domain || '')
			.trim()
			.toUpperCase();
		if (!domain.includes('.')) return respond(null, 'A valid AD DNS domain is required.');
		return respond({
			domain,
			workgroup: domain.split('.')[0],
			site: 'Default-First-Site-Name',
			domainController: `dc1.${domain.toLowerCase()}`,
			controllers: [`dc1.${domain.toLowerCase()}`, `dc2.${domain.toLowerCase()}`],
			warning: ''
		});
	}
	if (path.endsWith('/active-directory/status') || path.endsWith('/active-directory/check'))
		return respond({
			state: state.ad.enabled && state.ad.joined ? 'HEALTHY' : 'DISABLED',
			joined: state.ad.joined,
			enabled: state.ad.enabled,
			domain: state.ad.domain,
			error: '',
			domainController: `dc1.${state.ad.domain.toLowerCase()}`,
			site: state.ad.site || 'Default-First-Site-Name',
			alternateController: `dc2.${state.ad.domain.toLowerCase()}`,
			alternateVerified: state.ad.joined,
			warning: state.ad.joinWarning,
			checkedAt: new Date().toISOString()
		});
	if (path === '/directory-services/principals') {
		if (!state.ad.enabled || !state.ad.joined) return respond(null, 'AD is not active');
		const query = (parsed.searchParams.get('q') ?? '').toLowerCase();
		return respond(
			demoDirectoryPrincipals.filter(
				(principal) =>
					principal.kind === parsed.searchParams.get('kind') &&
					principal.name.toLowerCase().includes(query)
			)
		);
	}
	if (path.endsWith('/kerberos/realms/discover')) {
		const realm = typeof body.realm === 'string' ? body.realm.trim().toUpperCase() : '';
		if (!realm) return respond(null, 'Realm is required');
		return respond({
			realm,
			kdc: [`dc1.${realm.toLowerCase()}`],
			adminServer: [],
			kpasswdServer: [`dc1.${realm.toLowerCase()}`]
		});
	}
	if (path.endsWith('/kerberos/settings')) {
		if (method === 'PUT') {
			const result = KerberosSettingsSchema.safeParse(body);
			if (!result.success) return respond(null, 'Invalid settings');
			state.settings = result.data;
		}
		return respond(state.settings);
	}
	const realmMatch = path.match(/\/kerberos\/realms(?:\/(\d+))?$/);
	if (realmMatch) {
		const id = realmMatch[1]
			? Number(realmMatch[1])
			: Math.max(0, ...state.realms.map((realm) => realm.id)) + 1;
		if (method === 'GET') return respond(state.realms);
		if (method === 'DELETE') {
			if (id === state.ad.realmId) return respond(null, 'AD references this realm');
			state.realms = state.realms.filter((realm) => realm.id !== id);
		} else {
			const result = KerberosRealmSchema.safeParse({
				...body,
				realm: typeof body.realm === 'string' ? body.realm.trim().toUpperCase() : body.realm,
				id
			});
			if (!result.success) return respond(null, 'Invalid realm');
			state.realms = [...state.realms.filter((realm) => realm.id !== id), result.data];
		}
		return respond(null);
	}
	const keytabMatch = path.match(/\/kerberos\/keytabs(?:\/(\d+))?$/);
	if (keytabMatch) {
		if (method === 'GET') return respond(state.keytabs);
		const id = keytabMatch[1]
			? Number(keytabMatch[1])
			: Math.max(0, ...state.keytabs.map((keytab) => keytab.id)) + 1;
		const existing = state.keytabs.find((keytab) => keytab.id === id);
		if (existing?.managed || id === state.ad.keytabId)
			return respond(null, 'AD requires this keytab');
		if (method === 'DELETE') state.keytabs = state.keytabs.filter((keytab) => keytab.id !== id);
		else {
			if (typeof body.name !== 'string') return respond(null, 'Name required');
			state.keytabs = [
				...state.keytabs.filter((keytab) => keytab.id !== id),
				{
					id,
					name: body.name,
					managed: false,
					principals: existing?.principals ?? ['joinuser@EXAMPLE.COM'],
					updatedAt: new Date().toISOString()
				}
			];
		}
		return respond(null);
	}
	return respond(null, 'Unknown directory services operation');
}
