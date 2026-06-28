import { z } from 'zod/v4';

export const TunableSchema = z.object({
    name: z.string(),
    value: z.string(),
    type: z.string(),
    writable: z.boolean()
});

export type Tunable = z.infer<typeof TunableSchema>;
