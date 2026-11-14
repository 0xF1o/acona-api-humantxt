// Regenerates the expected parser output that the Go tests compare against, so
// those tests fail the moment the bundle stops behaving like Postlight Parser
// under Node:  make goldens
import { readFile, writeFile } from 'node:fs/promises'
import process from 'node:process'
import Parser from '@postlight/parser/dist/mercury.js'

const fixtures = [
  { name: 'article', url: 'https://example.com/news/story/' },
  { name: 'empty', url: 'https://example.com/empty/' }
]

const formats = ['text', 'markdown', 'html']

for (const fixture of fixtures) {
  const html = (await readFile(new URL(`../internal/vm/testdata/fixtures/${fixture.name}.html`, import.meta.url))).toString()

  for (const format of formats) {
    const result = await Parser.parse(fixture.url, { html, contentType: format })
    const target = new URL(`../internal/vm/testdata/golden/${fixture.name}.${format}.json`, import.meta.url)
    await writeFile(target, `${JSON.stringify(result, null, 2)}\n`)
    process.stdout.write(`${fixture.name}.${format}\n`)
  }
}
