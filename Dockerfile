FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /shiron . && mkdir /data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /shiron /shiron
# The bot runs as the unprivileged "nonroot" user (65532), so it must own its data folder.
COPY --from=build --chown=65532:65532 /data /data
WORKDIR /data
ENV SHIRON_DB=/data/shiron.db
VOLUME /data
ENTRYPOINT ["/shiron"]
