import { expect, test } from "bun:test";
import { handler } from "./handler.ts";

const get = (qs: string) => handler(new Request(`http://localhost/?${qs}`));

test("missing url", async () => {
  const res = await get("");
  expect(res.status).toBe(400);
  expect(await res.text()).toBe("Parameter 'url' not found");
});

test("invalid url", async () => {
  const res = await get("url=%2Ffoo");
  expect(res.status).toBe(400);
  expect(await res.text()).toBe("Invalid url: '/foo'");
});

test("invalid format", async () => {
  const res = await get("url=https%3A%2F%2Forf.at&format=xml");
  expect(res.status).toBe(400);
  expect(await res.text()).toBe("Invalid format: 'xml'");
});

const page = `<html><head><title>Fixture</title></head><body><article><h1>Fixture Title</h1><p>${"lorem ipsum ".repeat(40)}</p></article></body></html>`;

test("post html body", async () => {
  const res = await handler(new Request("http://localhost/?url=https%3A%2F%2Fexample.com%2Fa%2Fb", { method: "POST", body: page }));
  expect(res.status).toBe(200);
  const body = await res.json();
  expect(body.title).toBe("Fixture Title");
  expect(body.content).toStartWith("lorem ipsum");
});

test("post empty body", async () => {
  const res = await handler(new Request("http://localhost/?url=https%3A%2F%2Fexample.com", { method: "POST", body: "" }));
  expect(res.status).toBe(400);
  expect(await res.text()).toBe("Empty request body");
});
