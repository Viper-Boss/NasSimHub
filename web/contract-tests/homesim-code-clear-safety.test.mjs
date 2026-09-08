import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('clearing the code shelf never deletes conversations or ordinary messages', async () => {
  const source = await readFile(new URL('../src/components/SmsCodeShelf.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(source, /deleteMessageThreads|deleteThread|deleteMessage\(/)
  assert.match(source, /dismissed\.value = new Set/)
  assert.match(source, /filter\(item => !dismissed\.value\.has\(item\.key\)\)/)
})
