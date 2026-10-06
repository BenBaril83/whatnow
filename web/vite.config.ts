import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// Every route falls back to one shell page; the Go server serves it for any unknown path.
			adapter: adapter({ fallback: '200.html' })
		})
	],
	server: {
		// `just serve` runs the API here. The SSE stream goes through the same proxy.
		proxy: { '/api': 'http://127.0.0.1:7777' }
	},
	test: {
		expect: { requireAssertions: true },
		include: ['src/**/*.test.ts'],
		environment: 'node'
	}
});
