// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

import { z } from 'zod/v4';

export const HostInterfaceL3AddressSchema = z.object({
	family: z.string(),
	address: z.string(),
	prefixLength: z.number()
});

export const HostInterfaceL3ManagedAddressSchema = z.object({
	family: z.string(),
	address: z.string()
});

export const HostInterfaceL3EntrySchema = z.object({
	interface: z.string(),
	vlanParent: z.string().default(''),
	vlanTag: z.number().default(0),
	ipv6Mode: z.string().default('inherit'),
	mtu: z.number().nullable().optional(),
	mtuBaseline: z.number().nullable().optional(),
	metric: z.number().nullable().optional(),
	metricBaseline: z.number().nullable().optional(),
	identityMac: z.string().default(''),
	revision: z.number().default(1),
	addresses: z.array(HostInterfaceL3AddressSchema).default([]),
	managedAddresses: z.array(HostInterfaceL3ManagedAddressSchema).default([]),
	present: z.boolean().default(false),
	liveMac: z.string().default(''),
	conflicts: z.array(z.string()).default([])
});

export const HostInterfaceL3TargetSchema = z.object({
	interface: z.string(),
	eligible: z.boolean().default(false),
	reason: z.string().default('')
});

export const HostInterfaceL3ListSchema = z.object({
	rows: z.array(HostInterfaceL3EntrySchema).default([]),
	targets: z.array(HostInterfaceL3TargetSchema).default([])
});

export const HostInterfaceL3PendingEntrySchema = z.object({
	id: z.string(),
	interface: z.string(),
	kind: z.string(),
	phase: z.string(),
	deadline: z.string()
});

export type HostInterfaceL3Address = z.infer<typeof HostInterfaceL3AddressSchema>;
export type HostInterfaceL3ManagedAddress = z.infer<typeof HostInterfaceL3ManagedAddressSchema>;
export type HostInterfaceL3Entry = z.infer<typeof HostInterfaceL3EntrySchema>;
export type HostInterfaceL3Target = z.infer<typeof HostInterfaceL3TargetSchema>;
export type HostInterfaceL3List = z.infer<typeof HostInterfaceL3ListSchema>;
export type HostInterfaceL3PendingEntry = z.infer<typeof HostInterfaceL3PendingEntrySchema>;

export function emptyHostInterfaceL3List(): HostInterfaceL3List {
	return { rows: [], targets: [] };
}

export function isHostInterfaceL3List(value: unknown): value is HostInterfaceL3List {
	if (typeof value !== 'object' || value === null) {
		return false;
	}
	const candidate = value as { rows?: unknown; targets?: unknown };
	return Array.isArray(candidate.rows) && Array.isArray(candidate.targets);
}
