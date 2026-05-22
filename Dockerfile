FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/qrsgen ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/qrsgen /app/qrsgen
COPY --from=build /src/assets /app/assets
EXPOSE 3100
USER nonroot:nonroot
ENTRYPOINT ["/app/qrsgen"]
