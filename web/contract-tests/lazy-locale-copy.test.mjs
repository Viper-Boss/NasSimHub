import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { readdirSync } from 'node:fs'
import test from 'node:test'

import { homesimEN, homesimZH } from '../src/i18n/homesimData.ts'
import { messageGuardMessages } from '../src/i18n/messageGuardMessages.ts'
import { dhtPanelMessages } from '../src/i18n/dhtPanelMessages.ts'
import {
  globeCanvasLabel,
  globeScopeNote,
  meshCanvasLabel,
  meshNote,
  networkCardLabels
} from '../src/i18n/networkCardNotes.ts'
import englishLocale from '../src/i18n/locales/en-US.ts'
import chineseLocale from '../src/i18n/locales/zh-CN.ts'

// Copy that would push the startup bundle over its budget is moved out of the
// locale files and merged in by the view that needs it. That move is silent
// when it goes wrong: a key that lives in neither place renders as its own
// dotted name, which looks like a stray debug string rather than an error, and
// nothing in the build or the type checker notices. These tests are the thing
// that notices.

// eachKey walks a nested message object into dotted paths.
function eachKey(value, prefix, out) {
  for (const [name, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${name}` : name
    if (child && typeof child === 'object' && !Array.isArray(child)) {
      eachKey(child, path, out)
    } else {
      out.add(path)
    }
  }
  return out
}

// referencedKeys finds every t('...') lookup under one prefix.
function referencedKeys(source, prefix) {
  const found = new Set()
  const pattern = new RegExp(`t\\(\\s*['\`]((?:${prefix})\\.[A-Za-z0-9_.]+)['\`]`, 'g')
  for (const match of source.matchAll(pattern)) {
    found.add(match[1])
  }
  return found
}

// What ships in the payload every visitor downloads: the locale file, plus the
// shared homesim block each locale file pulls in.
const startup = {
  en: { ...englishLocale, homesim: { ...englishLocale.homesim, ...homesimEN } },
  zh: { ...chineseLocale, homesim: { ...chineseLocale.homesim, ...homesimZH } }
}

const cases = [
  {
    what: 'the SMS fraud guard',
    view: 'src/views/MessagesView.vue',
    prefix: 'homesim\\.fraudGuard',
    lazy: {
      en: { homesim: { fraudGuard: messageGuardMessages('en-US') } },
      zh: { homesim: { fraudGuard: messageGuardMessages('zh-CN') } }
    }
  },
  {
    what: 'the DHT panel',
    view: 'src/components/settings/DHTPanel.vue',
    prefix: 'dht',
    lazy: {
      en: { dht: dhtPanelMessages('en-US') },
      zh: { dht: dhtPanelMessages('zh-CN') }
    }
  },
  {
    what: 'the DHT mesh card',
    view: 'src/components/DHTMeshCard.vue',
    prefix: 'dashboard',
    lazy: {
      en: {
        dashboard: {
          ...networkCardLabels('en-US'),
          meshNote: meshNote('en-US'),
          meshCanvasLabel: meshCanvasLabel('en-US')
        }
      },
      zh: {
        dashboard: {
          ...networkCardLabels('zh-CN'),
          meshNote: meshNote('zh-CN'),
          meshCanvasLabel: meshCanvasLabel('zh-CN')
        }
      }
    }
  },
  {
    what: 'the network globe',
    view: 'src/components/NetworkGlobeCard.vue',
    prefix: 'dashboard',
    lazy: {
      en: {
        dashboard: {
          ...networkCardLabels('en-US'),
          globeScopeNote: globeScopeNote('en-US'),
          globeCanvasLabel: globeCanvasLabel('en-US')
        }
      },
      zh: {
        dashboard: {
          ...networkCardLabels('zh-CN'),
          globeScopeNote: globeScopeNote('zh-CN'),
          globeCanvasLabel: globeCanvasLabel('zh-CN')
        }
      }
    }
  }
]

test('the mesh note is translated into every locale the app ships', () => {
  // Falling back to English here would be invisible: the card still renders a
  // paragraph, just the wrong one, and only a reader of that language would
  // ever notice.
  const shipped = readdirSync(new URL('../src/i18n/locales', import.meta.url))
    .filter(name => name.endsWith('.ts'))
    .map(name => name.replace(/\.ts$/, ''))
  assert.ok(shipped.length >= 9, `only found ${shipped.length} locales`)
  for (const locale of shipped) {
    assert.notEqual(
      meshNote(locale),
      locale === 'en-US' ? '' : meshNote('en-US'),
      `${locale} falls back to the English mesh note`
    )
  }
})

test('every string that moved out of the locale files is translated', () => {
  // The mesh note above is one paragraph; these are the twenty short labels and
  // the two canvas descriptions that moved out of the locale files to keep the
  // startup budget. Same failure, and just as invisible: the card still renders
  // a badge, in English, and only a reader of that language ever notices.
  const shipped = readdirSync(new URL('../src/i18n/locales', import.meta.url))
    .filter(name => name.endsWith('.ts'))
    .map(name => name.replace(/\.ts$/, ''))
  const english = networkCardLabels('en-US')
  const keys = Object.keys(english)
  assert.ok(keys.length >= 20, `only ${keys.length} labels moved; the list is incomplete`)
  for (const locale of shipped) {
    if (locale === 'en-US') continue
    const labels = networkCardLabels(locale)
    assert.deepEqual(
      Object.keys(labels).sort(),
      keys.slice().sort(),
      `${locale} is missing some of the card labels`
    )
    // Latin-script locales legitimately share a word with English here and
    // there, so the test is that the SET is not the English set wholesale --
    // which is what a missing locale falling through would give.
    assert.notDeepEqual(labels, english, `${locale} falls back to the English card labels`)
    for (const [key, value] of Object.entries(labels)) {
      assert.ok(value.trim().length > 0, `${locale}.${key} is empty`)
    }
  }
  for (const locale of shipped) {
    if (locale === 'en-US') continue
    assert.notEqual(
      globeCanvasLabel(locale),
      globeCanvasLabel('en-US'),
      `${locale} falls back to the English globe description`
    )
    assert.notEqual(
      meshCanvasLabel(locale),
      meshCanvasLabel('en-US'),
      `${locale} falls back to the English mesh description`
    )
  }
})

for (const example of cases) {
  test(`every key ${example.what} renders resolves in English and Chinese`, async () => {
    const source = await readFile(new URL(`../${example.view}`, import.meta.url), 'utf8')
    const used = referencedKeys(source, example.prefix)
    assert.ok(
      used.size > 0,
      `found no t() lookups under ${example.prefix} in ${example.view}; the pattern has drifted`
    )

    for (const language of ['en', 'zh']) {
      const available = eachKey(startup[language], '', new Set())
      eachKey(example.lazy[language], '', available)

      for (const key of used) {
        assert.ok(
          available.has(key),
          `${key} is used by ${example.view} but exists in neither the startup ` +
            `locale nor the lazily merged copy (${language}); it would render as ` +
            'its own key name'
        )
      }
    }
  })
}

test('the lazily merged guard copy stays out of the startup locale', () => {
  // The whole point of the move was the bundle budget. If these keys come back
  // into homesimData they are paid for on every page load again, and the budget
  // check only notices once something else pushes it over the line.
  const startup = eachKey({ homesim: homesimEN }, '', new Set())
  for (const key of Object.keys(messageGuardMessages('en-US'))) {
    assert.ok(
      !startup.has(`homesim.fraudGuard.${key}`),
      `homesim.fraudGuard.${key} is back in the startup locale; it belongs in ` +
        'messageGuardMessages so it is not downloaded before the app draws'
    )
  }
})

test('both languages of the guard copy carry the same keys', () => {
  // A key present in English and missing in Chinese falls back to English
  // silently, which reads as a translation that was simply never done.
  assert.deepEqual(
    Object.keys(messageGuardMessages('zh-CN')).sort(),
    Object.keys(messageGuardMessages('en-US')).sort()
  )
})
