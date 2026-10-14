ARG BASE_IMAGE=forgejo.develop.10.199.64.20.nip.io/easy-platform/toolchain-base:latest
FROM ${BASE_IMAGE}
ARG EASYLAB=http://easy-platform.temp.svc.cluster.local
RUN curl -fsSL -o /tmp/go.tar.gz "${EASYLAB}/artifacts/generic/go/1.27.1/go1.27.1.linux-amd64.tar.gz" \
 && tar -xzf /tmp/go.tar.gz -C /opt && rm /tmp/go.tar.gz
ENV PATH=/opt/go/bin:$PATH
