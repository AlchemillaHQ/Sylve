import { z } from 'zod/v4';
import type { Row } from '../components/tree-table';

const nullableString = z
	.string()
	.nullish()
	.transform((value) => value ?? '');

export const StaticRouteSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	description: nullableString,
	enabled: z
		.boolean()
		.nullish()
		.transform((value) => value ?? true),
	fib: z.number().int().nonnegative(),
	destinationType: z.enum(['host', 'network']),
	destination: z.string(),
	destinationRaw: z.string().optional().default(''),
	destinationObjId: z.number().int().positive().nullable().optional().default(null),
	family: z.enum(['inet', 'inet6']),
	nextHopMode: z.enum(['gateway', 'interface']),
	gateway: nullableString,
	gatewayRaw: z.string().optional().default(''),
	gatewayObjId: z.number().int().positive().nullable().optional().default(null),
	gatewayZone: nullableString,
	interface: nullableString,
	createdAt: z.string(),
	updatedAt: z.string()
});

export const StaticRouteSuggestionSchema = z.object({
	name: z.string(),
	description: nullableString,
	enabled: z
		.boolean()
		.nullish()
		.transform((value) => value ?? true),
	fib: z.number().int().nonnegative(),
	destinationType: z.enum(['host', 'network']),
	destination: z.string(),
	family: z.enum(['inet', 'inet6']),
	nextHopMode: z.enum(['gateway', 'interface']),
	gateway: nullableString,
	gatewayZone: nullableString,
	interface: nullableString,
	sourceHint: nullableString
});

export type StaticRouteRow = StaticRoute &
	Row & {
		interfaceFriendlyName: string;
		nextHop: string;
	};

export type StaticRoute = z.infer<typeof StaticRouteSchema>;
export type StaticRouteSuggestion = z.infer<typeof StaticRouteSuggestionSchema>;
export type StaticRouteUpsertRequest = Omit<StaticRoute, 'id' | 'createdAt' | 'updatedAt'>;
