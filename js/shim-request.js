import { Buffer } from 'buffer/'

// The parser's HTTP client. Only the Go host opens connections, so every
// request goes through the function it registers as __fetch: for the url handed
// to extract() and for page 2 and beyond of an article.
export default function request (options, callback) {
  const headers = {}

  for (const [name, value] of Object.entries(options.headers || {})) {
    headers[name.toLowerCase()] = String(value)
  }

  let reply

  try {
    reply = JSON.parse(globalThis.__fetch(options.url, JSON.stringify(headers)))
  } catch (error) {
    callback(error)
    return
  }

  if (reply.error) {
    callback(new Error(reply.error))
    return
  }

  const binary = atob(reply.body)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)

  // the parser always asks for encoding: null, i.e. an unparsed Buffer
  callback(null, {
    statusCode: reply.status,
    statusMessage: reply.statusText,
    headers: reply.headers
  }, Buffer.from(bytes))
}
