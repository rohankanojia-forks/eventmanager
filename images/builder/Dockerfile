# Build the manager binary
# FROM registry.access.redhat.com/ubi8/go-toolset:1.17.7 as builder
FROM registry.access.redhat.com/ubi9/go-toolset:1.22.9-1743094161 AS builder

USER root
WORKDIR /workspace
COPY . .
RUN make build

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1742914212

LABEL MAINTAINER="Adrian Riobo <ariobolo@redhat.com>"

COPY --from=builder /workspace/out/eventmanager /workspace/images/builder/entrypoint.sh /usr/local/bin/

ENTRYPOINT ["entrypoint.sh"]
