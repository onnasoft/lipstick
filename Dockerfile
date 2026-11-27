# Build stage: compile the server only. The client is built separately, on the
# machine where the local service runs.
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/lipstick ./server

# Runtime stage: no shell and no package manager, and it runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lipstick /usr/local/bin/lipstick

# The configuration and the certificates are not baked into the image. Mount a
# directory at /etc/lipstick that contains config.yml. It must be writable by
# uid 65532 when the certificates do not exist yet, because they are generated
# at startup into /etc/lipstick/certs.
#
#   docker run -v /srv/lipstick:/etc/lipstick -p 5050:5050 -p 5051:5051 lipstick
#
# The admin API listens on 127.0.0.1 inside the container by default, so it is
# not reachable from the host. Use the host or a shell in the container to reach it.
EXPOSE 5050 5051

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/lipstick"]
CMD ["-c", "/etc/lipstick/config.yml"]
