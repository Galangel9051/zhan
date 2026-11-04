import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = join(root, 'frontend/dist')
const target = join(root, 'backend/internal/webui/dist')
if (!existsSync(join(source, 'index.html'))) throw new Error('Run the frontend build first')
mkdirSync(target, { recursive: true })
// Only remove generated embedded assets; preserve the tracked placeholder.
for (const name of readdirSync(target)) if (name !== '.gitkeep') rmSync(join(target, name), { recursive: true, force: true })
cpSync(source, target, { recursive: true })
