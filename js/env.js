// Environment shims. Imported first and must stay import-free so that it runs
// before any bundled library touches these globals.
// QuickJS (wasm build used by github.com/fastschema/qjs) has no atob/Intl/URL.

const BASE64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'

if (typeof atob !== 'function') {
  globalThis.atob = (input) => {
    const str = String(input).replace(/=+$/, '')
    let bits = 0, held = 0, out = ''
    for (const char of str) {
      const index = BASE64.indexOf(char)
      if (index === -1) throw new Error('InvalidCharacterError')
      bits = (bits << 6) | index
      if ((held += 6) >= 8) {
        held -= 8
        out += String.fromCharCode((bits >>> held) & 0xff)
        bits &= (1 << held) - 1
      }
    }
    return out
  }
}

// linkedom's CSS support formats numbers through Intl at module load time.
if (typeof Intl !== 'object') {
  globalThis.Intl = {
    NumberFormat: class { format (value) { return String(value) } },
    Collator: class { compare (a, b) { return String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0 } }
  }
}
