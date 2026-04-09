import { z } from 'zod/v4';

const nullableString = z
	.string()
	.nullish()
	.transform((value) => value ?? '');

export const StaticRouteSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	description: nullableString,
	enabled: z.boolean().nullish().transform((value) => value ?? true),
	fib: z.number().int().nonnegative(),
	destinationType: z.enum(['host', 'network']),
	destination: z.string(),
	family: z.enum(['inet', 'inet6']),
	nextHopMode: z.enum(['gateway', 'interface']),
	gateway: nullableString,
	interface: nullableString,
	createdAt: z.string(),
	updatedAt: z.string()
});

export const StaticRouteSuggestionSchema = z.object({
	name: z.string(),
	description: nullableString,
	enabled: z.boolean().nullish().transform((value) => value ?? true),
	fib: z.number().int().nonnegative(),
	destinationType: z.enum(['host', 'network']),
	destination: z.string(),
	family: z.enum(['inet', 'inet6']),
	nextHopMode: z.enum(['gateway', 'interface']),
	gateway: nullableString,
	interface: nullableString,
	sourceHint: nullableString
});

export type StaticRoute = z.infer<typeof StaticRouteSchema>;
export type StaticRouteSuggestion = z.infer<typeof StaticRouteSuggestionSchema>;
