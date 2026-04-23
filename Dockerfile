ARG  BUILDER_IMAGE=golang:1.23.1-alpine3.20
############################
# STEP 1 build executable binary
############################
FROM ${BUILDER_IMAGE} AS builder

# Install git + SSL ca certificates.
# Git is required for fetching the dependencies.
# Ca-certificates is required to call HTTPS endpoints.
RUN apk update && apk add --no-cache git ca-certificates tzdata gcc musl-dev && update-ca-certificates

# Create appuser
ENV USER=appuser
ENV UID=10001

# See https://stackoverflow.com/a/55757473/12429735
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    "${USER}"

    WORKDIR $GOPATH/src/github.com/OpenDataTelemetry/mqtt-topic-rewrite-lns-imt/
COPY . .

# Fetch dependencies.
RUN go get -v

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
    -tags musl \
    -ldflags='-w -s -extldflags "-static"' -a \
    -o /go/bin/parse-mqtt-to-influxdb3 .

############################
# STEP 2 build a small image
############################
FROM scratch

# Import from builder.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy our static executable
COPY --from=builder /go/bin/parse-mqtt-to-influxdb3 /go/bin/parse-mqtt-to-influxdb3

# Use an unprivileged user.
USER appuser:appuser

# Run the parse-mqtt-to-influxdb3 binary.
ENTRYPOINT ["/go/bin/parse-mqtt-to-influxdb3"]
