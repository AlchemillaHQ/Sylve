import { z } from 'zod/v4';
import { APIResponseSchema } from '$lib/types/common';
import {
	ActiveDirectorySchema,
	ADStatusSchema,
	ADDiscoverySchema,
	DirectoryPrincipalSchema,
	KerberosRealmSchema,
	KerberosRealmInputSchema,
	KerberosKeytabSchema,
	KerberosSettingsSchema,
	type ActiveDirectoryInput,
	type KerberosRealmInput,
	type KerberosSettings
} from '$lib/types/directory-services';
import { apiRequestData, apiRequestResult, type NodeAPIDataRequestOptions } from '$lib/utils/http';

const root = '/directory-services';
export const getActiveDirectory = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/active-directory`, ActiveDirectorySchema, 'GET', undefined, options);
export const getADStatus = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/active-directory/status`, ADStatusSchema, 'GET', undefined, options);
export const checkADStatus = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/active-directory/check`, ADStatusSchema, 'POST', undefined, options);
export const discoverActiveDirectory = (
	input: { domain: string; site: string; domainController: string },
	options?: NodeAPIDataRequestOptions
) => apiRequestData(`${root}/active-directory/discover`, ADDiscoverySchema, 'POST', input, options);
export const getDirectoryRecoveryInfo = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/recovery`, z.object({ pending: z.boolean() }), 'GET', undefined, {
		...options,
		sensitive: true
	});
export const exportDirectoryRecovery = (
	passphrase: string,
	confirmComputerName: string,
	manageServices: boolean,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestData(
		`${root}/recovery/export`,
		z.object({ file: z.string(), warning: z.string().default('') }),
		'POST',
		{ passphrase, confirmComputerName, manageServices },
		{ ...options, sensitive: true }
	);
export const restoreDirectoryRecovery = (
	passphrase: string,
	confirmComputerName: string,
	file: string,
	manageServices: boolean,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		`${root}/recovery/restore`,
		APIResponseSchema,
		'POST',
		{ passphrase, confirmComputerName, file, manageServices },
		{ ...options, sensitive: true }
	);
export const rollbackDirectoryRecovery = (
	passphrase: string,
	confirmComputerName: string,
	manageServices: boolean,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		`${root}/recovery/rollback`,
		APIResponseSchema,
		'POST',
		{ passphrase, confirmComputerName, manageServices },
		{ ...options, sensitive: true }
	);
export const updateActiveDirectory = (
	input: ActiveDirectoryInput,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(`${root}/active-directory`, APIResponseSchema, 'PUT', input, {
		...options,
		sensitive: true
	});
export const setADEnabled = (enabled: boolean, options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(
		`${root}/active-directory/enabled`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
export const changeADMembership = (
	leave: boolean,
	username: string,
	password: string,
	localOnly = false,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		`${root}/active-directory/${leave ? 'leave' : 'join'}`,
		APIResponseSchema,
		'POST',
		{ username, password, ...(leave ? { localOnly } : {}) },
		{ ...options, sensitive: true }
	);
export const searchDirectoryPrincipals = (
	query: string,
	kind: 'user' | 'group',
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestData(
		`${root}/principals?q=${encodeURIComponent(query)}&kind=${kind}`,
		z.array(DirectoryPrincipalSchema),
		'GET',
		undefined,
		options
	);
export const getKerberosRealms = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(
		`${root}/kerberos/realms`,
		z.array(KerberosRealmSchema),
		'GET',
		undefined,
		options
	);
export const discoverKerberosRealm = (realm: string, options?: NodeAPIDataRequestOptions) =>
	apiRequestData(
		`${root}/kerberos/realms/discover`,
		KerberosRealmInputSchema,
		'POST',
		{ realm },
		options
	);
export const saveKerberosRealm = (
	id: number | null,
	input: KerberosRealmInput,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		`${root}/kerberos/realms${id === null ? '' : `/${id}`}`,
		APIResponseSchema,
		id === null ? 'POST' : 'PUT',
		input,
		options
	);
export const deleteKerberosRealm = (id: number, options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(
		`${root}/kerberos/realms/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
export const getKerberosKeytabs = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(
		`${root}/kerberos/keytabs`,
		z.array(KerberosKeytabSchema),
		'GET',
		undefined,
		options
	);
export const saveKerberosKeytab = (
	id: number | null,
	name: string,
	file: string,
	options?: NodeAPIDataRequestOptions
) =>
	apiRequestResult(
		`${root}/kerberos/keytabs${id === null ? '' : `/${id}`}`,
		APIResponseSchema,
		id === null ? 'POST' : 'PUT',
		{ name, file },
		{ ...options, sensitive: true }
	);
export const deleteKerberosKeytab = (id: number, options?: NodeAPIDataRequestOptions) =>
	apiRequestResult(
		`${root}/kerberos/keytabs/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
export const getKerberosSettings = (options?: NodeAPIDataRequestOptions) =>
	apiRequestData(`${root}/kerberos/settings`, KerberosSettingsSchema, 'GET', undefined, options);
export const updateKerberosSettings = (
	input: KerberosSettings,
	options?: NodeAPIDataRequestOptions
) => apiRequestResult(`${root}/kerberos/settings`, APIResponseSchema, 'PUT', input, options);
