import z from "zod/v4";

export const CloudInitTemplateSchema = z.object({
    id: z.number().int().nonnegative(),
    name: z.string(),
    user: z.string(),
    meta: z.string(),
    networkConfig: z.string(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type CloudInitTemplate = z.infer<typeof CloudInitTemplateSchema>;