// What one document costs the parser under Node, for the numbers in the README:
// the same sizes are measured against QuickJS through the Go tests.
import Parser from '@postlight/parser/dist/mercury.js'

const article = (bytes) => {
  let body = ''
  for (let i = 0; body.length < bytes; i++) body += `<p>Sentence number ${i} about ${'lorem ipsum dolor sit amet consectetur '.repeat(4)}.</p>`
  return `<html><head><title>Big</title></head><body><article><h1>Big</h1>${body}</article></body></html>`
}

for (const size of [100_000, 250_000, 500_000]) {
  const html = article(size)
  const start = process.hrtime.bigint()
  const resource = await Parser.parse('http://localhost/', { html, contentType: 'text' })
  console.log(`${html.length} bytes  ${(Number(process.hrtime.bigint() - start) / 1e6).toFixed(0)} ms  ${resource.word_count} words`)
}
