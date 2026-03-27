FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
WORKDIR /app/cmd/server/edge
RUN go build -o /edge

WORKDIR /app/cmd/tools/monitor
RUN go build -o /monitor

WORKDIR /app/cmd/tools/uhnctl
RUN go build -o /uhnctl

WORKDIR /app/cmd/tools/sandbox-launch
RUN go build -o /uhn-sandbox-launch

WORKDIR /app/cmd/tools/sandbox-setup
RUN go build -o /uhn-sandbox-setup

WORKDIR /app/cmd/tools/sandboxd
RUN go build -o /uhn-sandboxd

# Sandbox tools image — use as: docker build --target sandbox-tools
FROM alpine:3.20 AS sandbox-tools
COPY --from=build /uhn-sandbox-launch /usr/lib/uhn/uhn-sandbox-launch
COPY --from=build /uhn-sandbox-setup /usr/lib/uhn/uhn-sandbox-setup
COPY --from=build /uhn-sandboxd /usr/lib/uhn/uhn-sandboxd
CMD ["echo", "sandbox tools image — use as volume source"]

# Edge runtime
FROM alpine:3.20
RUN apk add --no-cache nodejs npm

ENV UHN_EDGE_CONFIG_PATH=/config/edge-config.json \
    UHN_MQTT_URL=tcp://mosquitto:1883 \
    UHN_EDGE_NAME=edge1 \
    UHN_WORKSPACE_PATH=/uhn-workspace \
    UHN_SANDBOX_PATH=/usr/lib/uhn \
    UHN_NODE_PATH=/usr/bin \
    UHN_RUNTIME_PATH=/uhn-runtime/packages/uhn-rule-runtime

# Go binaries
COPY --from=build /edge /edge
COPY --from=build /monitor /monitor
COPY --from=build /uhnctl /uhnctl

# Sandbox tools (from local build)
COPY --from=build /uhn-sandbox-launch /usr/lib/uhn/uhn-sandbox-launch
COPY --from=build /uhn-sandbox-setup /usr/lib/uhn/uhn-sandbox-setup
COPY --from=build /uhn-sandboxd /usr/lib/uhn/uhn-sandboxd

# Rule runtime (from UXP utility image in registry)
COPY --from=localhost:5000/uhn-runtime:latest /uhn-runtime /uhn-runtime

ENTRYPOINT ["/edge"]
