FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/Alexander-D-Karpov/calendar/internal/buildinfo.Version=${VERSION} -X github.com/Alexander-D-Karpov/calendar/internal/buildinfo.Commit=${COMMIT} -X github.com/Alexander-D-Karpov/calendar/internal/buildinfo.Date=${DATE}" \
    -o /out/calendar ./cmd/calendar \
 && mkdir -p /out/data/og

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/calendar /calendar
COPY --from=build --chown=65532:65532 /out/data /data
ENV HTTP_ADDR=:8080 OG_CACHE_DIR=/data/og
EXPOSE 8080
ENTRYPOINT ["/calendar"]
CMD ["serve"]