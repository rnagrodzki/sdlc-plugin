import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';

export const collections = {
  docs: defineCollection({ loader: glob({ base: '../docs', pattern: ['**/*.md', '!internal/**'] }) }),
  root: defineCollection({ loader: glob({ base: '..', pattern: ['README.md', 'CHANGELOG.md'] }) }),
};
