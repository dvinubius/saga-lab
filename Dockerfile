FROM golang:1.27.0-alpine3.24@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG SERVICE
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/${SERVICE}

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
COPY --from=build /out/service /usr/local/bin/service
USER nobody
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/service"]
