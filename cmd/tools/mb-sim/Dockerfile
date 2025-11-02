FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
WORKDIR /app/cmd/tools/mb-sim
RUN go build -o /mb-sim

FROM alpine:3.20
ENV MB_LISTEN_ADDR=:1502
COPY --from=build /mb-sim /mb-sim
EXPOSE 1502
ENTRYPOINT ["/mb-sim"]
