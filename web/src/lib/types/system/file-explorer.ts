import { z } from 'zod/v4';

export const FileNodeSchema = z.object({
	id: z.string(),
	date: z.string().transform((s) => new Date(s)),
	type: z.string(),
	lazy: z.boolean().optional(),
	size: z.number().min(0).optional(),
	mode: z.string().optional(),
	owner: z.string().optional(),
	group: z.string().optional(),
	link: z.string().optional(),
	broken: z.boolean().optional(),
	special: z.string().optional(),
	mount: z.string().optional(),
	protected: z.string().optional()
});

export type FileNode = z.infer<typeof FileNodeSchema>;

export const FileListingSchema = z.object({
	entries: FileNodeSchema.array(),
	total: z.number(),
	matched: z.number(),
	truncated: z.boolean()
});

export type FileListing = z.infer<typeof FileListingSchema>;

export const DownloadTicketSchema = z.object({
	ticket: z.string(),
	expiresAt: z.string()
});

export type DownloadTicket = z.infer<typeof DownloadTicketSchema>;
