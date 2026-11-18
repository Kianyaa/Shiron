FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /shiron .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /shiron /shiron
WORKDIR /data
ENV SHIRON_DB=/data/shiron.db
VOLUME /data
ENTRYPOINT ["/shiron"]
