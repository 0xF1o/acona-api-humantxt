import Parser from "@postlight/parser";

const FORMATS = new Set(["text", "markdown", "html"]);
const VALID_URL = /^(?:https?:\/\/)?(?:[^@/\n]+@)?(?:www\.)?[^:/\n]+/;
const bad = (msg: string) => new Response(msg, { status: 400 });

export async function handler(req: Request): Promise<Response> {
  const q = new URL(req.url).searchParams;
  const url = q.get("url");
  if (!url) return bad("Parameter 'url' not found");
  if (!VALID_URL.test(url)) return bad(`Invalid url: '${url}'`);
  const format = q.get("format") || "text";
  if (!FORMATS.has(format)) return bad(`Invalid format: '${format}'`);
  const html = req.method === "POST" ? await req.text() : undefined;
  if (html === "") return bad("Empty request body");
  try {
    const result = await Parser.parse(url, { contentType: format as "text", html });
    return Response.json(result);
  } catch (e) {
    return bad(String(e));
  }
}
