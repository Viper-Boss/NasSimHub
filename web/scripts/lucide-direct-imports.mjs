// Rewrites `import { Phone, X as Close } from '@lucide/vue'` into one import
// per icon file, so an icon used only by a lazily loaded view stays in that
// view's chunk instead of the startup shell.
//
// Why a build step and not a coding rule: `@lucide/vue`'s entry is a barrel
// that statically re-exports every icon. The shell imports icons through it,
// so the barrel is part of the startup graph -- and with it every icon it
// re-exports, including the ones only the settings panels use. Rolldown's
// `$initial` tag then marks all of them as startup code and the `icons` group
// ships all of them in the shell. Importing each icon's own file takes the
// barrel out of the graph; `$initial` then means what it says.
//
// A name that is not an icon fails the build instead of quietly bringing the
// barrel back, because the barrel coming back costs nothing visible except
// several KiB of startup, and nobody would notice.
import { readFileSync } from 'node:fs'

const barrelURL = new URL('../node_modules/@lucide/vue/dist/esm/lucide-vue.mjs', import.meta.url)
const importPattern = /import\s*\{([^}]*)\}\s*from\s*(['"])@lucide\/vue\2;?/g

let iconFiles

function iconTable() {
  if (iconFiles) return iconFiles
  iconFiles = new Map()
  const barrel = readFileSync(barrelURL, 'utf8')
  for (const [, names, file] of barrel.matchAll(
    /export \{([^}]*)\} from '\.\/icons\/([a-z0-9-]+)\.mjs'/g
  )) {
    for (const [, name] of names.matchAll(/default as (\w+)/g)) {
      iconFiles.set(name, file)
    }
  }
  if (iconFiles.size === 0) {
    throw new Error(`lucide-direct-imports: no icons found in ${barrelURL.pathname}; did @lucide/vue change its layout?`)
  }
  return iconFiles
}

/** Rewrites one module's source; exported for the contract test. */
export function rewriteLucideImports(code, fail = message => { throw new Error(message) }) {
  if (!code.includes('@lucide/vue')) return null
  const table = iconTable()
  let changed = false
  const out = code.replace(importPattern, (statement, body) => {
    const imports = []
    for (const raw of body.split(',')) {
      const specifier = raw.trim()
      if (!specifier) continue
      const [imported, local = imported] = specifier.split(/\s+as\s+/).map(part => part.trim())
      const file = table.get(imported)
      if (!file) {
        fail(`lucide-direct-imports: '${imported}' is not an icon of @lucide/vue; import it from its own file instead of the barrel`)
        return statement
      }
      imports.push(`import ${local} from '@lucide/vue/dist/esm/icons/${file}.mjs';`)
    }
    changed = true
    // Same number of lines as the statement it replaces, so line numbers in
    // errors and source maps still point at the right place.
    const lines = statement.split('\n').length
    return imports.join(' ') + '\n'.repeat(lines - 1)
  })
  return changed ? out : null
}

export function lucideDirectImports() {
  return {
    name: 'lucide-direct-imports',
    enforce: 'pre',
    transform(code, id) {
      // The whole .vue file arrives here once, before the Vue plugin splits
      // it into ?vue&type=... requests; those sub-requests are left alone.
      if (id.includes('?') || !/[\\/]src[\\/].+\.(?:vue|ts)$/.test(id)) return null
      const rewritten = rewriteLucideImports(code, message => this.error(message))
      return rewritten === null ? null : { code: rewritten, map: null }
    }
  }
}
