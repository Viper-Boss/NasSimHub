import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { rewriteLucideImports } from '../scripts/lucide-direct-imports.mjs'

// The startup budget depends on icons NOT reaching the shell through
// @lucide/vue's barrel, which re-exports every icon. These pin the rewrite
// that keeps it out, and the config that relies on it.

test('each icon is imported from its own file, aliases kept', () => {
  const out = rewriteLucideImports(
    "import { Phone, X as Close } from '@lucide/vue'\nconst a = 1\n"
  )
  assert.equal(
    out,
    "import Phone from '@lucide/vue/dist/esm/icons/phone.mjs'; " +
      "import Close from '@lucide/vue/dist/esm/icons/x.mjs';\nconst a = 1\n"
  )
  assert.doesNotMatch(out, /from '@lucide\/vue'/)
})

test('a multi-line import keeps its line count', () => {
  const source = "import {\n  Phone,\n  Download\n} from '@lucide/vue'\nconst marker = 1\n"
  const out = rewriteLucideImports(source)
  assert.equal(out.split('\n').indexOf('const marker = 1'), source.split('\n').indexOf('const marker = 1'))
})

test('a name that is not an icon fails instead of bringing the barrel back', () => {
  assert.throws(
    () => rewriteLucideImports("import { createLucideIcon } from '@lucide/vue'\n"),
    /not an icon/
  )
})

test('source without the barrel is left alone', () => {
  assert.equal(rewriteLucideImports("import { ref } from 'vue'\n"), null)
})

test('every icon the app imports resolves to a file', async () => {
  const { readdir } = await import('node:fs/promises')
  const files = []
  async function walk(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir)
      if (entry.isDirectory()) await walk(path)
      else if (/\.(vue|ts)$/.test(entry.name)) files.push(path)
    }
  }
  await walk(new URL('../src/', import.meta.url))
  let rewritten = 0
  for (const file of files) {
    const out = rewriteLucideImports(await readFile(file, 'utf8'))
    if (out !== null) rewritten += 1
  }
  assert.ok(rewritten > 50, `only ${rewritten} files import icons; has the import style changed?`)
})

test('the build uses the rewrite and splits icons by startup', async () => {
  const config = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')
  assert.match(config, /lucideDirectImports\(\),\s*\n\s*vue\(\)/)
  assert.match(config, /name: 'icons',[\s\S]*?tags: \['\$initial'\],[\s\S]*?name: 'icons-lazy'/)
})
