import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema, i18nSchema } from '@astrojs/starlight/schema';

export const collections = {
	i18n: defineCollection({ loader: glob({ pattern: '**/*.{json,yaml}', base: './src/content/i18n' }), schema: i18nSchema() }),
	docs: defineCollection({
		loader: docsLoader(),
		schema: docsSchema()
	}),
	blog: defineCollection({
		loader: glob({ pattern: '**/[^_]*.{md,mdx}', base: "./src/content/blog" }),
		schema: z.object({
			title: z.string(),
			description: z.string(),
			pubDate: z.coerce.date(),
			author: z.string(),
			layout: z.string().optional(),
			// Add dummy sidebar to prevent Starlight crashes if it scans this collection
			sidebar: z.object({
				hidden: z.boolean().default(false),
				label: z.string().optional(),
			}).optional().default({ hidden: false }),
		}),
	}),
};
