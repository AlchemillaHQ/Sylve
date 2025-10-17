import { z } from 'zod/v4';

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
	type: z.string(),
	url: z.string(),
	progress: z.number(),
	size: z.number(),
	files: z.array(DownloadedFileSchema),
	uType: z.string().optional(),
	extractedPath: z.string().optional(),
	error: z.string().optional(),
    status: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export type Download = z.infer<typeof DownloadSchema>;
export type DownloadedFile = z.infer<typeof DownloadedFileSchema>;
