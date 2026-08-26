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
	uid: z.number().optional().default(0),
	shell: z.string().optional().default('/usr/sbin/nologin'),
	homeDirectory: z.string().optional().default('/nonexistent'),
	homeDirPerms: z.number().optional().default(493),
	sshPublicKey: z.string().optional().default(''),
	disablePassword: z.boolean().optional().default(false),
	locked: z.boolean().optional().default(false),
	doasEnabled: z.boolean().optional().default(false),
	primaryGroupId: z.number().nullable().optional(),
	source: z.enum(['local', 'pam']).default('local'),
	passkeyEligible: z.boolean(),
	createdAt: z.string(),
	updatedAt: z.string(),
	lastLoginTime: z.string(),
	groups: z
		.array(
			z.object({
				id: z.number().int(),
				name: z.string(),
				notes: z.string().optional().default('')
			})
		)
		.optional()
});

export const GroupSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	notes: z.string(),
	createdAt: z.string(),
	updatedAt: z.string(),
	users: z.array(UserSchema).optional()
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

export const BasicSettingsSchema = z.object({
	pools: z.array(z.string()).nullable().default([]),
	services: z
		.array(
			z.enum([
				'dhcp-server',
				'samba-server',
				'virtualization',
				'jails',
				'wol-server',
				'firewall',
				'wireguard'
			])
		)
		.nullable()
		.default([]),
	initialized: z.boolean()
});

export type JWTClaims = z.infer<typeof JWTClaimsSchema>;
export type User = z.infer<typeof UserSchema>;
export type Group = z.infer<typeof GroupSchema>;
export type GroupMutationResult = z.infer<typeof GroupMutationResultSchema>;
export type Passkey = z.infer<typeof PasskeySchema>;
export type UserMutationResult = z.infer<typeof UserMutationResultSchema>;
export type SambaAction = z.infer<typeof SambaActionSchema>;
export type NextUIDResult = z.infer<typeof NextUIDResultSchema>;
export type UserCapabilities = z.infer<typeof UserCapabilitiesSchema>;
export type ImportableUnixUser = z.infer<typeof ImportableUnixUserSchema>;
