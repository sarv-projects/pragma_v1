import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/ws': { target: 'http://localhost:3777', ws: true },
			'/api': { target: 'http://localhost:3777' }
		}
	}
});
