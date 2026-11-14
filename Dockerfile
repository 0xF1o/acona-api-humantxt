FROM oven/bun:1-alpine AS build
RUN apk add --no-cache git
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY mw-data-url.patch /tmp/
RUN cd node_modules/@postlight/parser && git apply -v /tmp/mw-data-url.patch
COPY server.ts handler.ts ./
RUN bun build --compile --minify server.ts --outfile /humantxt

FROM alpine:3
RUN apk add --no-cache ca-certificates libstdc++
COPY --from=build /humantxt /app/humantxt
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/app/humantxt"]
