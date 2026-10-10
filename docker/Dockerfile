ARG BUILDER_IMAGE=golang:1.24-alpine

############################
# STEP 1 build executable binary
############################
FROM ${BUILDER_IMAGE} AS builder

# ca-certificates for HTTPS, tzdata for timezone support
RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates

# Create unprivileged appuser
ENV USER=appuser
ENV UID=10001

RUN adduser \
  --disabled-password \
  --gecos "" \
  --home "/nonexistent" \
  --shell "/sbin/nologin" \
  --no-create-home \
  --uid "${UID}" \
  "${USER}"

WORKDIR /build

# Download dependencies first (layer cached until go.mod/go.sum change)
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go build — no CGO needed
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags='-w -s' \
  -o /go/bin/parse-mqtt-to-influxdb3 ./cmd/parse-mqtt-to-influxdb3/

############################
# STEP 2 minimal scratch image
############################
FROM scratch

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

COPY --from=builder /go/bin/parse-mqtt-to-influxdb3 /go/bin/parse-mqtt-to-influxdb3

USER appuser:appuser

# REGISTRY_API_URL (the SmartCampusMaua `api` repo's registry endpoints) is
# deploy-specific, same as MQTT_BROKER/INFLUXDB_HOST below — set via env_file
# in docker-compose.yml or the k8s manifest's env/configMap, not baked in here.

ENTRYPOINT ["/go/bin/parse-mqtt-to-influxdb3"]
