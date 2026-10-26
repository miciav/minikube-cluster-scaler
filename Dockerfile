# syntax=docker/dockerfile:1
# Lab image: provider, demo scripts, manifests and observer. minikube and
# kubectl are not included: the `lab` wrapper mounts the ones from the VM.

FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
COPY proto ./proto
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/minikube-externalgrpc-provider ./cmd/provider

# minikube's docker driver shells out to the docker CLI.
FROM docker:29.9.2-cli AS docker-cli

FROM python:3.13-slim-trixie
ARG LAB_IMAGE_TAG=dev
LABEL org.opencontainers.image.source="https://github.com/miciav/minikube-cluster-scaler" \
      org.opencontainers.image.description="minikube Cluster Autoscaler lab"
# pip is only needed to install rich; dropping it removes its vendored
# packages (urllib3, msgpack, ...) from the image and from Trivy's scope.
RUN pip install --no-cache-dir rich==14.3.4 \
    && pip uninstall -y pip \
    && rm -rf /usr/local/lib/python3.13/ensurepip
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/minikube-externalgrpc-provider /usr/local/bin/minikube-externalgrpc-provider
COPY scripts /opt/lab/scripts
COPY deploy /opt/lab/deploy
ENV LAB_CONTAINER=1 \
    LAB_IMAGE_TAG=$LAB_IMAGE_TAG \
    PYTHONDONTWRITEBYTECODE=1
WORKDIR /opt/lab
ENTRYPOINT ["/opt/lab/scripts/lab-entrypoint.sh"]
CMD ["help"]
