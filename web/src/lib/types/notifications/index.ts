import { z } from 'zod/v4';

export const NotificationSeveritySchema = z.enum(['info', 'warning', 'error', 'critical']);

export const NotificationSchema = z.object({
    id: z.number(),
    kind: z.string(),
    title: z.string(),
    body: z.string(),
    severity: NotificationSeveritySchema,
    source: z.string(),
    fingerprint: z.string(),
    metadata: z.record(z.string(), z.string()).default({}),
    occurrenceCount: z.number(),
    firstOccurredAt: z.string(),
    lastOccurredAt: z.string(),
    dismissedAt: z.string().nullable().optional(),
    createdAt: z.string().optional(),
    updatedAt: z.string().optional()
});

export const NotificationsListSchema = z.object({
    items: z.array(NotificationSchema),
    total: z.number()
});

export const NotificationsCountSchema = z.object({
    active: z.number()
});

export const NotificationConfigSchema = z.object({
    ntfy: z.object({
        enabled: z.boolean(),
        baseUrl: z.string(),
        topic: z.string(),
        hasAuthToken: z.boolean()
    }),
    email: z.object({
        enabled: z.boolean(),
        smtpHost: z.string(),
        smtpPort: z.number(),
        smtpUsername: z.string(),
        smtpFrom: z.string(),
        smtpUseTls: z.boolean(),
        recipients: z.array(z.string()),
        hasPassword: z.boolean()
    })
});

export type Notification = z.infer<typeof NotificationSchema>;
export type NotificationsList = z.infer<typeof NotificationsListSchema>;
export type NotificationsCount = z.infer<typeof NotificationsCountSchema>;
export type NotificationConfig = z.infer<typeof NotificationConfigSchema>;

export type UpdateNotificationConfigInput = {
    ntfy: {
        enabled: boolean;
        baseUrl: string;
        topic: string;
        authToken?: string;
    };
    email: {
        enabled: boolean;
        smtpHost: string;
        smtpPort: number;
        smtpUsername: string;
        smtpFrom: string;
        smtpUseTls: boolean;
        recipients: string[];
        smtpPassword?: string;
    };
};
