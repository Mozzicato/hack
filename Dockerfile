# ---- build ----
FROM --platform=linux/amd64 golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
# Fully static, stripped binary. CGO off => runs on scratch.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w" -o app .

# ---- run (scratch: ~6-8MB final image) ----
FROM scratch
COPY --from=builder /app/app /app
EXPOSE 8080
ENV PORT=8080
CMD ["/app"]
