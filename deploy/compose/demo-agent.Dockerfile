# The OTel-instrumented demo agent (cmd/demo-agent), for agent.yaml.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /demo-agent ./cmd/demo-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /demo-agent /demo-agent
ENTRYPOINT ["/demo-agent"]
