FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /out/humantxt ./cmd/humantxt

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/humantxt /humantxt
ENV ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/humantxt"]
