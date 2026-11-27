import { z } from 'zod/v4';
import { GroupSchema, UserSchema } from '../auth';
import { DirectoryPrincipalSchema } from '../directory-services';

export const SambaPrincipalUserSchema = UserSchema.pick({
	id: true,
	username: true
});

export const SambaPrincipalGroupSchema = GroupSchema.pick({
	id: true,
	name: true
});

export const SambaPrincipalSetSchema = z.object({
	users: z.array(SambaPrincipalUserSchema).default([]),
	groups: z.array(SambaPrincipalGroupSchema).default([]),
	directory: z.array(DirectoryPrincipalSchema).default([])
});

export const SambaPermissionsSchema = z.object({
	read: SambaPrincipalSetSchema,
	write: SambaPrincipalSetSchema
});

export const SambaGuestSchema = z.object({
	enabled: z.boolean(),
	writeable: z.boolean()
});

export const SharePermissionJobSchema = z.object({
	state: z.enum(['running', 'completed', 'failed', 'cancelled']),
	processed: z.number(),
	failed: z.number(),
	startedAt: z.string(),
	finishedAt: z.string().optional(),
	error: z.string().optional()
});

export type SharePermissionJob = z.infer<typeof SharePermissionJobSchema>;

export const SambaShareSchema = z.object({
	id: z.number(),
	name: z.string(),
	dataset: z.string(),
	enabled: z.boolean().default(true),
	permissions: SambaPermissionsSchema,
	guest: SambaGuestSchema,
	createMask: z.string(),
	directoryMask: z.string(),
	timeMachine: z.boolean().default(false),
	timeMachineMaxSize: z.number().default(0),
	auditEnabled: z.boolean().default(false),
	auditRetentionDays: z.number().int().min(0).default(70),
	auditedOperations: z.array(z.string()).default([]),
	extraShareConfig: z.string().default(''),
	createdAt: z.string(),
	updatedAt: z.string(),
	datasetName: z.string().default(''),
	mountpoint: z.string().default(''),
	state: z.string().optional(),
	datasetBlock: z.string().optional(),
	permissionsJob: SharePermissionJobSchema.optional()
});

export type SambaShare = z.infer<typeof SambaShareSchema>;

export const ShareDatasetSchema = z.object({
	guid: z.string(),
	name: z.string(),
	mountpoint: z.string(),
	mounted: z.boolean(),
	sharedBy: z.object({ id: z.number(), name: z.string() }).optional(),
	block: z.string().optional()
});

export type ShareDataset = z.infer<typeof ShareDatasetSchema>;

export const SambaPrincipalsSchema = z.object({
	users: SambaPrincipalUserSchema.extend({ sambaAccount: z.boolean().nullable() }).array(),
	groups: SambaPrincipalGroupSchema.array()
});

export type SambaPrincipals = z.infer<typeof SambaPrincipalsSchema>;

export const SambaStatusSchema = z.object({
	serviceEnabled: z.boolean(),
	running: z.boolean(),
	version: z.string(),
	sessions: z
		.object({
			id: z.string(),
			pid: z.string(),
			username: z.string(),
			group: z.string(),
			remoteMachine: z.string(),
			hostname: z.string(),
			dialect: z.string(),
			encryption: z.string(),
			signing: z.string()
		})
		.array(),
	connections: z
		.object({
			share: z.string(),
			sessionId: z.string(),
			pid: z.string(),
			machine: z.string(),
			connectedAt: z.string(),
			encryption: z.string(),
			signing: z.string()
		})
		.array(),
	openFiles: z.number(),
	error: z.string().optional()
});

export type SambaStatus = z.infer<typeof SambaStatusSchema>;

export type SambaShareInput = {
	name: string;
	dataset: string;
	enabled: boolean;
	permissions: {
		read: { userIds: number[]; groupIds: number[]; directorySids: string[] };
		write: { userIds: number[]; groupIds: number[]; directorySids: string[] };
	};
	guest: { enabled: boolean; writeable: boolean };
	createMask: string;
	directoryMask: string;
	timeMachine: boolean;
	timeMachineMaxSize: number;
	auditEnabled: boolean;
	auditRetentionDays: number;
	auditedOperations: string[];
	extraShareConfig: string;
	applyToExisting: boolean;
};
