import { handler } from "./handler.ts";

const port = Number(process.env.PORT ?? 8080);
Bun.serve({ port, fetch: handler, maxRequestBodySize: 10 * 1024 * 1024 });
console.log(`listening on :${port}`);
