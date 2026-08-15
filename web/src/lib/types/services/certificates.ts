import { z } from 'zod/v4';

export const CERTIFICATE_NAME_MAX_LENGTH = 128;
export const CERTIFICATE_DOMAIN_MAX_LENGTH = 253;
export const CERTIFICATE_PEM_MAX_BYTES = 1 << 20;

export const CertificateTypeSchema = z.enum([
	'imported',
	'self-signed',
	'lets-encrypt',
	'sylve-managed',
	'system-default'
]);

export const CertificateIssuanceStatusSchema = z.enum([
	'ready',
	'submitting',
	'queued',
	'processing',
	'blocked',
	'failed'
]);

export const CertificateIssuanceOperationSchema = z.enum(['', 'initial', 'renewal']);

export const CertificateSchema = z.object({
	id: z.number().int().positive(),
	name: z.string().max(CERTIFICATE_NAME_MAX_LENGTH),
	type: CertificateTypeSchema,
	domain: z.string().max(CERTIFICATE_DOMAIN_MAX_LENGTH),
	dynamicDnsEntryId: z.number().int().positive().nullable(),
	staging: z.boolean(),
	fingerprint: z.string().nullable(),
	notBefore: z.string().nullable(),
	notAfter: z.string().nullable(),
	updatedAt: z.string(),
	active: z.boolean(),
	pending: z.boolean(),
	ready: z.boolean(),
	renewable: z.boolean(),
	issuanceStatus: CertificateIssuanceStatusSchema,
	issuanceOperation: CertificateIssuanceOperationSchema,
	issuanceError: z.string(),
	issuanceRetryAt: z.string().nullable().optional()
});

export const CertificateDomainCheckSchema = z.object({
	domain: z.string(),
	resolved: z
		.array(z.string())
		.nullable()
		.transform((value) => value ?? []),
	publicAddresses: z
		.array(z.string())
		.nullable()
		.transform((value) => value ?? []),
	matches: z.boolean(),
	warning: z.string()
});

export type Certificate = z.infer<typeof CertificateSchema>;
export type CertificateType = z.infer<typeof CertificateTypeSchema>;
export type CertificateDomainCheck = z.infer<typeof CertificateDomainCheckSchema>;

export interface CertificateInput {
	name: string;
	type: Exclude<CertificateType, 'system-default'>;
	domain: string;
	dynamicDnsEntryId?: number;
	staging: boolean;
	validateDomain: boolean;
	certificate?: string;
	privateKey?: string;
}
