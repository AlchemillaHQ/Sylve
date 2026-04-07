import { z } from 'zod/v4';

export const SambaConfigSchema = z.object({
    id: z.number(),
    unixCharset: z.string(),
    workgroup: z.string(),
    serverString: z.string().default('Sylve SMB Server'),
    interfaces: z.string().default('lo0'),
    bindInterfacesOnly: z.boolean().default(true),
    appleExtensions: z.boolean().default(false)
});

export type SambaConfig = z.infer<typeof SambaConfigSchema>;
