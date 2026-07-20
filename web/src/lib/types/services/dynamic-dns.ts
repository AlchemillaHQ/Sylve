import { z } from 'zod/v4';

export const DynamicDNSEntrySchema = z.object({
	id: z.number(),
	enabled: z.boolean(),
	provider: z.string(),
	providerSettings: z.record(z.string(), z.string()).nullish().transform((value) => value ?? {}),
	credentialConfigured: z.boolean(),
	hostname: z.string(),
	recordType: z.enum(['A', 'AAAA', 'BOTH']),
	intervalMinutes: z.number(),
	sourceType: z.enum(['interface', 'manual', 'stun']),
	sourceSettings: z.record(z.string(), z.string()).nullish().transform((value) => value ?? {}),
	lastStatus: z.string().default(''),
	lastError: z.string().default(''),
	ipv4Status: z.string().default(''),
	ipv4Error: z.string().default(''),
	ipv6Status: z.string().default(''),
	ipv6Error: z.string().default(''),
	lastIPv4: z.string().default(''),
	lastIPv6: z.string().default(''),
	lastSyncAt: z.string().nullable().optional(),
	lastSuccessAt: z.string().nullable().optional(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export type DynamicDNSEntry = z.infer<typeof DynamicDNSEntrySchema>;

export interface DynamicDNSEntryInput {
	enabled: boolean;
	provider: 'cloudflare' | 'namecheap';
	providerSettings?: Record<string, string>;
	token?: string;
	hostname: string;
	recordType: 'A' | 'AAAA' | 'BOTH';
	intervalMinutes: number;
	sourceType: 'interface' | 'manual' | 'stun';
	sourceSettings: Record<string, string>;
}
