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


FROM alpine:3.20
ENV EDGE_CONFIG_PATH=/config/edge-config.json \
    MQTT_URL=tcp://mosquitto:1883 \
    EDGE_NAME=edge1
COPY --from=build /edge /edge
COPY --from=build /monitor /monitor
COPY --from=build /uhnctl /uhnctl
COPY --from=build /app/config/edge-config-dev.json /config/edge-config-dev.json
ENTRYPOINT ["/edge"]
