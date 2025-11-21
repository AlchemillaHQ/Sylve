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
	uType: z.enum(['base-rootfs', 'cloud-init', 'uncategorized', '']),
	extractedPath: z.string().optional(),
	error: z.string().optional(),
	status: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const UTypeGroupedDownloadSchema = z.object({
	uuid: z.string(),
	label: z.string(),
	uType: z.enum(['base-rootfs', 'cloud-init', 'uncategorized'])
});

export type Download = z.infer<typeof DownloadSchema>;
export type DownloadedFile = z.infer<typeof DownloadedFileSchema>;
export type UTypeGroupedDownload = z.infer<typeof UTypeGroupedDownloadSchema>;
