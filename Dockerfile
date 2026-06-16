# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s" \
    -o triage-bot .

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates git nodejs npm bash
# claude-code intentionally unpinned — we want CLI updates (bug fixes, model support).
# MCP server pinned — a version change broke config discovery (see setupMCPConfig).
RUN npm install -g @anthropic-ai/claude-code @aashari/mcp-server-atlassian-jira@3.3.0

# GID 0 (root group) for OCP restricted SCC compatibility.
RUN adduser -u 1001 -G root -s /bin/sh -D appuser

WORKDIR /app
COPY --from=builder /app/triage-bot .
COPY --from=builder /app/task.tmpl .

RUN mkdir -p /opt/workflows /tmp/triage-workspace /tmp/triage-output /home/appuser && \
    chown -R 1001:0 /app /opt/workflows /tmp/triage-workspace /tmp/triage-output /home/appuser && \
    chmod -R g=u /app /opt/workflows /tmp/triage-workspace /tmp/triage-output /home/appuser

ENV HOME=/home/appuser
ENV SHELL=/bin/bash
USER 1001
EXPOSE 8080

ENTRYPOINT ["./triage-bot"]
