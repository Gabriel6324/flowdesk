ARG NODE_IMAGE=node:24.19.0-bookworm-slim
ARG GO_IMAGE=golang:1.27.2-bookworm
FROM ${NODE_IMAGE} AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
COPY internal/domain/guides.json /src/internal/domain/guides.json
RUN npm run build

FROM ${GO_IMAGE} AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
ARG SOURCE_HASH=unrecorded
ARG BUILT_AT=unrecorded
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X flowdesk/internal/buildinfo.SourceHash=${SOURCE_HASH} -X flowdesk/internal/buildinfo.BuiltAt=${BUILT_AT}" \
    -o /flowdesk ./cmd/flowdesk

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 flowdesk && useradd -u 10001 -g 10001 -M flowdesk \
    && mkdir /data && chown 10001:10001 /data
COPY --from=backend /flowdesk /usr/local/bin/flowdesk
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/flowdesk/
COPY licenses /usr/share/doc/flowdesk/licenses
USER 10001:10001
VOLUME ["/data"]
ENV FLOWDESK_DATA_DIR=/data
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=4s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/flowdesk", "--healthcheck", "http://127.0.0.1:8080/api/ready"]
ENTRYPOINT ["/usr/local/bin/flowdesk"]
CMD ["--profile", "server", "--listen", "0.0.0.0:8080", "--no-browser"]

