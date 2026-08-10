import { z } from 'zod/v4';

export const DownloadUTypeSchema = z.enum(['base-rootfs', 'cloud-init', 'uncategorized']);
export const DownloadStatusSchema = z.enum(['pending', 'processing', 'done', 'failed']);

export const DownloadedFileSchema = z.object({
	id: z.number(),
	downloadId: z.number(),
	name: z.string(),
	size: z.number()
});

export const DownloadSchema = z.object({
	id: z.number(),
	uuid: z.string(),
	path: z.string(),
	name: z.string(),
	type: z.enum(['http', 'torrent', 'path']),
	url: z.string(),
	progress: z.number(),
	size: z.number(),
	files: z.array(DownloadedFileSchema),
	uType: DownloadUTypeSchema.or(z.literal('')),
	extractedPath: z.string().optional(),
	error: z.string().optional(),
	status: DownloadStatusSchema,
	automaticExtraction: z.boolean(),
	automaticRawConversion: z.boolean(),
	ignoreTLS: z.boolean(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const UTypeGroupedDownloadSchema = z.object({
	uuid: z.string(),
	label: z.string(),
	uType: DownloadUTypeSchema
});

export const DownloadStartResultSchema = z.object({
	id: z.number().int().positive(),
	status: z.literal('pending')
});

export const DownloadDeleteItemSchema = z.object({
	id: z.number().int().positive(),
	uuid: z.string(),
	name: z.string(),
	type: z.enum(['http', 'torrent', 'path'])
});

export const DownloadDeleteVMReferenceSchema = z.object({
	storageId: z.number().int().positive(),
	vmId: z.number().int().nonnegative(),
	vmRid: z.number().int().nonnegative(),
	vmName: z.string()
});

export const DownloadDeleteFailureSchema = z.object({
	id: z.number().int().positive(),
	uuid: z.string(),
	name: z.string(),
	type: z.enum(['http', 'torrent', 'path']).or(z.literal('')),
	code: z.string(),
	retainedPaths: z.array(z.string()),
	vmReferences: z.array(DownloadDeleteVMReferenceSchema)
});

export const DownloadDeleteResultSchema = z.object({
	deleted: z.array(DownloadDeleteItemSchema),
	failed: z.array(DownloadDeleteFailureSchema)
});

export const SignedDownloadURLResultSchema = z.object({
	url: z.string().min(1),
	expiresAt: z.string().min(1)
});

export const DownloaderUploadCompletionSchema = z.object({
	uploadId: z.string(),
	downloadId: z.number(),
	status: z.literal('completed')
});

export const DownloaderUploadAbortSchema = z.object({
	uploadId: z.string(),
	status: z.enum(['aborted', 'completed'])
});

export type Download = z.infer<typeof DownloadSchema>;
export type DownloadType = z.infer<typeof DownloadUTypeSchema>;
export type DownloadStartResult = z.infer<typeof DownloadStartResultSchema>;
export type DownloadDeleteResult = z.infer<typeof DownloadDeleteResultSchema>;
export type SignedDownloadURLResult = z.infer<typeof SignedDownloadURLResultSchema>;
export type DownloadedFile = z.infer<typeof DownloadedFileSchema>;
export type UTypeGroupedDownload = z.infer<typeof UTypeGroupedDownloadSchema>;
export type DownloaderUploadCompletion = z.infer<typeof DownloaderUploadCompletionSchema>;
export type DownloaderUploadAbort = z.infer<typeof DownloaderUploadAbortSchema>;
