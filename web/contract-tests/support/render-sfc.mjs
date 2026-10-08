// Renders a single-file component to HTML for a contract test.
//
// Most contract tests here read a component's SOURCE, which is the right tool
// for "this must never be written". It cannot answer "what does a reader see
// when the gateway sends X" -- and for the security block that is the question
// that matters: does a device with no evidence get a tick, does monitor mode
// read as protection. So this compiles the component the way Vite would and
// renders it on the server renderer the app already depends on.
//
// Limits, deliberately: nothing runs that needs a DOM (onMounted does not
// fire, so no poller starts and no request is made; clicks cannot be
// simulated). That fits a component that is a function of its props and of
// state a test can set before rendering.
//
// A component may import other components: each is compiled the same way and
// the import is pointed at the compiled copy. That is what lets a test render
// a whole card field -- several components deep -- and read what it says for
// one device, rather than asserting on source text that a card "would" show
// the right thing. Stylesheet imports are dropped: there is no layout here.
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'

import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { renderToString } from '@vue/server-renderer'
import { createSSRApp } from 'vue'

const sourceRoot = new URL('../../src/', import.meta.url)
// Inside node_modules so the compiled module resolves `vue` exactly as the
// source file does, and so nothing is ever written into src/.
const cacheRoot = new URL('../../node_modules/.cache/contract-sfc/', import.meta.url)

// One compilation per source file per process, so a component reached by two
// routes is one module (and one set of module-level state).
const compiledFiles = new Map()

/** Compiles one .vue file (and what it imports) and returns the output's URL. */
async function compileFile(file) {
  const known = compiledFiles.get(file.href)
  if (known) return known
  const pending = (async () => {
    const source = await readFile(file, 'utf8')
    const { descriptor, errors } = parse(source, { filename: file.pathname })
    if (errors.length) throw errors[0]
    // Per file, so two components' scoped-style hashes cannot collide.
    const id = createHash('sha256').update(file.href).digest('hex').slice(0, 8)
    // A component that is a template and nothing else (a named transition
    // around a slot) has no script for compileScript to work from.
    const content = descriptor.script || descriptor.scriptSetup
      ? compileScript(descriptor, { id, inlineTemplate: true, templateOptions: { ssr: true } }).content
      : `${compileTemplate({ source: descriptor.template?.content ?? '', filename: file.pathname, id, ssr: true, ssrCssVars: [] }).code}\nexport default { ssrRender }\n`
    let code = content.replace(/^\s*import\s+['"][^'"]+\.css['"];?\s*$/gm, '')
    // Relative imports are resolved against the component's own location,
    // since the compiled module lives somewhere else. Static and dynamic
    // forms both, the latter for defineAsyncComponent.
    const specifiers = new Set()
    for (const match of code.matchAll(/(?:from\s+|import\s*\(\s*)(['"])(\.{1,2}\/[^'"]+)\1/g)) {
      specifiers.add(match[2])
    }
    const resolved = new Map()
    for (const specifier of specifiers) {
      const target = new URL(specifier, file)
      if (target.pathname.endsWith('.vue')) {
        resolved.set(specifier, (await compileFile(target)).href)
      } else {
        resolved.set(
          specifier,
          (/\.[a-z]+$/.test(target.pathname) ? target : new URL(`${target.href}.ts`)).href
        )
      }
    }
    code = code.replace(
      /(from\s+|import\s*\(\s*)(['"])(\.{1,2}\/[^'"]+)\2/g,
      (_match, keyword, quote, specifier) => `${keyword}${quote}${resolved.get(specifier)}${quote}`
    )
    await mkdir(cacheRoot, { recursive: true })
    const name = `${createHash('sha256').update(code).digest('hex').slice(0, 16)}.mts`
    const output = new URL(name, cacheRoot)
    await writeFile(output, code)
    return output
  })()
  compiledFiles.set(file.href, pending)
  return pending
}

/** Compiles `src/<path>` and returns its component definition. */
export async function loadComponent(path) {
  const output = await compileFile(new URL(path, sourceRoot))
  return (await import(output.href)).default
}

/**
 * The component's HTML for one set of props. `plugins` are installed on the
 * app first -- the i18n instance, a router -- for components that read them.
 */
export function render(component, props, plugins = []) {
  const app = createSSRApp(component, props)
  for (const plugin of plugins) app.use(plugin)
  return renderToString(app)
}

/** Visible text: tags dropped, entities for the few characters Vue escapes. */
export function textOf(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/\s+/g, ' ')
    .trim()
}
