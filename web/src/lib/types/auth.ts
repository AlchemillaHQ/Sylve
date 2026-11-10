/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';

export const JWTClaimsSchema = z.object({
	exp: z.number(),
	jti: z.string(),
	custom_claims: z.object({
		userId: z.number(),
		username: z.string(),
		authType: z.string()
	})
});

export const UserSchema = z.object({
	id: z.number().int(),
	username: z.string(),
	fullName: z.string().optional().default(''),
	email: z.string().optional().default(''),
	notes: z.string(),
	admin: z.boolean(),
	confirmGuestActions: z.boolean().default(false),
	uid: z.number().optional().default(0),
	shell: z.string().optional().default('/usr/sbin/nologin'),
	homeDirectory: z.string().optional().default('/nonexistent'),
	homeDirPerms: z.number().optional().default(493),
	sshPublicKey: z.string().optional().default(''),
	disablePassword: z.boolean().optional().default(false),
	locked: z.boolean().optional().default(false),
	doasEnabled: z.boolean().optional().default(false),
	primaryGroupId: z.number().nullable().optional(),
	source: z.enum(['local', 'pam', 'oidc']).default('local'),
	externallyManaged: z.boolean().default(false),
	shared: z.boolean().default(false),
	nameTaken: z.boolean().default(false),
	passkeyEligible: z.boolean(),
	createdAt: z.string(),
	updatedAt: z.string(),
	lastLoginTime: z.string(),
	groups: z
		.array(
			z.object({
				id: z.number().int(),
				name: z.string(),
				externallyManaged: z.boolean().default(false),
				notes: z.string().optional().default('')
			})
		)
		.optional()
});

export const GroupSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	notes: z.string(),
	externallyManaged: z.boolean().default(false),
	createdAt: z.string(),
	updatedAt: z.string(),
	users: z.array(UserSchema).optional()
});

export const UserPreferencesSchema = z.object({
	confirmGuestActions: z.boolean()
});

export const GroupMutationResultSchema = z.object({
	id: z.number().int().positive(),
	name: z.string()
});

export const PasskeySchema = z.object({
	id: z.number().int().positive(),
	userId: z.number().int().positive(),
	credentialId: z.string().min(1),
	label: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const UserMutationResultSchema = z.object({
	id: z.number().int().positive(),
	username: z.string()
});

export const SambaActionSchema = z.enum(['keep', 'upsert', 'remove']);

export const NextUIDResultSchema = z.object({
	nextUID: z.number().int().min(1000).max(65533)
});

export const UserCapabilitiesSchema = z.object({
	doasAvailable: z.boolean()
});

export const ImportableUnixUserSchema = z.object({
	username: z.string(),
	fullName: z.string(),
	uid: z.number().int(),
	gid: z.number().int(),
	shell: z.string(),
	homeDirectory: z.string()
});

export type JWTClaims = z.infer<typeof JWTClaimsSchema>;
export type User = z.infer<typeof UserSchema>;
export type UserPreferences = z.infer<typeof UserPreferencesSchema>;
export type Group = z.infer<typeof GroupSchema>;
export type GroupMutationResult = z.infer<typeof GroupMutationResultSchema>;
export type Passkey = z.infer<typeof PasskeySchema>;
export type UserMutationResult = z.infer<typeof UserMutationResultSchema>;
export type SambaAction = z.infer<typeof SambaActionSchema>;
export type NextUIDResult = z.infer<typeof NextUIDResultSchema>;
export type UserCapabilities = z.infer<typeof UserCapabilitiesSchema>;
export type ImportableUnixUser = z.infer<typeof ImportableUnixUserSchema>;

export const ACLGrantSchema = z.object({
	id: z.number().int().optional(),
	path: z.string(),
	subjectType: z.enum(['user', 'group']),
	subject: z.string(),
	role: z.string(),
	propagate: z.boolean()
});

export const RoleSchema = z.object({
	name: z.string(),
	privileges: z.array(z.string()),
	builtin: z.boolean().default(false)
});

export const ACLGroupSchema = z.object({
	name: z.string(),
	members: z.array(z.string()).default([])
});

export const PathPrivilegesSchema = z.object({
	privileges: z.array(z.string()),
	below: z.array(z.string())
});

export const PermissionsSchema = z.object({
	superuser: z.boolean(),
	paths: z.record(z.string(), PathPrivilegesSchema),
	nodes: z.record(z.string(), z.string()).default({})
});

export const OIDCGroupMappingSchema = z.object({
	claim: z.string(),
	group: z.string()
});

export const AuthRealmSchema = z.object({
	name: z.string(),
	label: z.string(),
	issuer: z.string(),
	clientId: z.string(),
	hasSecret: z.boolean().default(false),
	scopes: z.string(),
	usernameClaim: z.string(),
	groupsClaim: z.string(),
	groupMappings: z.array(OIDCGroupMappingSchema).default([]),
	autoCreate: z.boolean(),
	queryUserInfo: z.boolean(),
	prompt: z.string(),
	acrValues: z.string(),
	hostedDomains: z.string().default(''),
	sessionHours: z.number().int(),
	caCert: z.string(),
	default: z.boolean(),
	enabled: z.boolean()
});

export const RealmDiscoverySchema = z.object({
	authorizationEndpoint: z.string(),
	tokenEndpoint: z.string(),
	userInfoEndpoint: z.string(),
	scopesSupported: z.array(z.string()).nullable().default([]),
	claimsSupported: z.array(z.string()).nullable().default([]),
	acrValuesSupported: z.array(z.string()).nullable().default([])
});

export type ACLGrant = z.infer<typeof ACLGrantSchema>;
export type Role = z.infer<typeof RoleSchema>;
export type ACLGroup = z.infer<typeof ACLGroupSchema>;
export type Permissions = z.infer<typeof PermissionsSchema>;
export type OIDCGroupMapping = z.infer<typeof OIDCGroupMappingSchema>;
export type AuthRealm = z.infer<typeof AuthRealmSchema>;
export type RealmDiscovery = z.infer<typeof RealmDiscoverySchema>;

// A realm user's username already ends in @<realm>.
export function subjectOf(user: Pick<User, 'username' | 'source'>): string {
	switch (user.source) {
		case 'oidc':
			return user.username;
		case 'pam':
			return `${user.username}@pam`;
		default:
			return `${user.username}@sylve`;
	}
}
