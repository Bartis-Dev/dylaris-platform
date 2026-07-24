import path from 'node:path'
import { defineConfig } from 'vitest/config'

export default defineConfig({
    resolve: {
        // Mirrors the "@/*" -> "./src/*" path alias in tsconfig.json so tests
        // can import modules (like lib/api/rcon.ts) that use it, same as the
        // Next.js build already does.
        alias: {
            '@': path.resolve(__dirname, './src'),
        },
    },
    test: {
        environment: 'node',
        include: ['src/**/*.test.ts'],
        passWithNoTests: true,
    },
})
