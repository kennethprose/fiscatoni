FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -o /fiscatoni ./cmd/fiscatoni

FROM mysql:8.4
COPY --from=build /fiscatoni /usr/local/bin/fiscatoni
