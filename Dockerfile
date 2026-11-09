# ---- build stage ----
FROM golang:1.25 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/notifier .

# ---- runtime stage ----
# distroless/static ships CA certs (needed to reach api.telegram.org) and tzdata.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /app/notifier /notifier
ENTRYPOINT ["/notifier"]
