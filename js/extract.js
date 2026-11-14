// The extraction tool: Postlight Parser running inside QuickJS (WebAssembly)
// instead of Node. The Go host calls:
//
//   extract(url, html, format)  ->  Promise<string> with the parser's JSON
//
// html may be empty, then the parser fetches the url itself through
// shim-request.js. url may be empty too (posted HTML), then a placeholder is
// used because the parser insists on a URL to pick its extractor and to make
// links absolute.
import './env.js'
import { NativePromise } from './node.js'
import Parser from '@postlight/parser/dist/mercury.js'

const PLACEHOLDER = 'http://localhost/'

function message (error) {
  const text = String((error && (error.message || error)) || '').trim()
  return text || 'the parser failed without a message'
}

function extract (url, html, format) {
  const requestedUrl = url || PLACEHOLDER

  return new NativePromise((resolve, reject) => {
    Parser.parse(requestedUrl, { html, contentType: format })
      .then((resource) => {
        if (!resource) reject(new Error('the parser returned no result'))

        if (!url && !resource.error) {
          resource.url = ''
          resource.domain = ''
        }

        // { error: true, message } passes through as a result: the CLI this
        // service replaced printed it and exited 0
        resolve(JSON.stringify(resource))
      }, (error) => reject(new Error(message(error))))
  })
}

globalThis.extract = extract
