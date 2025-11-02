FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
WORKDIR /app/cmd/server/edge
RUN go build -o /edge

FROM alpine:3.20
ENV MQTT_URL=tcp://mosquitto:1883
ENV MODBUS_TCP_ADDR=mb-sim:1502
ENV POLL_PERIOD_MS=1000
ENV UNIT_ID=1
COPY --from=build /edge /edge
ENTRYPOINT ["/edge"]
