// Bundles js/ into the single file the Go binary embeds:  make bundle
//
// platform=neutral on purpose: the parser's own "browser" field redirects
// cheerio to a jQuery shim that needs a real browser DOM, while Node's cheerio
// parses HTML by itself. Node built-ins the parser still expects are aliased to
// polyfills, or to a stub that can never be reached on purpose
// (postman-request): the Go host does all the fetching.
import { createRequire } from 'node:module'
import { dirname, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(import.meta.url)
const local = (file) => resolve(here, file)
const flags = process.argv.slice(2).filter((arg) => arg.startsWith('--'))
const positional = process.argv.slice(2).filter((arg) => !arg.startsWith('--'))

await build({
  entryPoints: [resolve(here, positional[0] || 'extract.js')],
  outfile: resolve(here, positional[1] || '../internal/vm/bundle/humantxt.js'),
  bundle: true,
  format: 'iife',
  platform: 'neutral',
  mainFields: ['main'],
  conditions: ['default'],
  target: 'es2020',
  minify: !flags.includes('--no-minify'),
  logLevel: 'warning',
  banner: { js: '/* Generated from js/ by "make bundle" or "npm run build" inside js/. Do not edit. */' },
  alias: {
    url: require.resolve('url/url.js'),
    stream: require.resolve('stream-browserify'),
    events: require.resolve('events/'),
    string_decoder: require.resolve('string_decoder/'),
    util: require.resolve('util/'),
    'postman-request': local('shim-request.js')
  },
  plugins: [{
    name: 'userland-punycode',
    setup: (bundle) => bundle.onResolve({ filter: /^punycode\/$/ }, () => ({ path: require.resolve('punycode/punycode.js') }))
  }]
})
