// Node built-ins Postlight Parser's Node build expects but QuickJS does not have.
import { Buffer as NodeBuffer } from 'buffer/'
import nodeProcess from 'process/browser.js'

// Captured before the parser's babel runtime replaces the global Promise with
// the core-js one, which the Go host cannot await.
export const NativePromise = globalThis.Promise

globalThis.Buffer = globalThis.Buffer || NodeBuffer
globalThis.process = globalThis.process || nodeProcess
