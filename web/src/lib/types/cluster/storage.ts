import { z } from 'zod/v4';

export const ClusterS3ConfigSchema = z.object({
	id: z.number(),
	name: z.string().min(2).max(100),
	endpoint: z.string(),
	region: z.string(),
	bucket: z.string(),
	accessKey: z.string(),
	secretKey: z.string()
});

export const ClusterDirectoryStorageConfigSchema = z.object({
	id: z.number(),
	name: z.string(),
	path: z.string()
});

export const ClusterStoragesSchema = z.object({
	s3: z.array(ClusterS3ConfigSchema).default([]),
	directories: z.array(ClusterDirectoryStorageConfigSchema).default([])
});

export type ClusterS3Config = z.infer<typeof ClusterS3ConfigSchema>;
export type ClusterDirectoryStorageConfig = z.infer<typeof ClusterDirectoryStorageConfigSchema>;
export type ClusterStorages = z.infer<typeof ClusterStoragesSchema>;
