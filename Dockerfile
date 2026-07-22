FROM hub-docker-mirrors.laiyouxi.com/library/node:24.18.0-alpine@sha256:4ba75f835bb8802193e4c114572113d4b26f95f6f094f4b5229d2a77773e0afc AS web-build
ARG NPM_REGISTRY=https://registry.npmmirror.com
ARG VITE_ASSETS_ENABLED=true
ENV VITE_ASSETS_ENABLED=${VITE_ASSETS_ENABLED}
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm config set registry "${NPM_REGISTRY}" && npm ci
COPY web/ ./
RUN npm run build

FROM hub-docker-mirrors.laiyouxi.com/library/golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS go-build
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY db/ db/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X cms/internal/version.Version=${VERSION} -X cms/internal/version.Commit=${COMMIT} -X cms/internal/version.BuildTime=${BUILD_TIME}" -o /out/cms ./cmd/cms

FROM hub-docker-mirrors.laiyouxi.com/library/alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
RUN addgroup -S cms && adduser -S -G cms cms
WORKDIR /app
COPY --from=go-build /out/cms /usr/local/bin/cms
COPY --from=web-build /src/web/dist /app/web
ENV APP_LISTEN_ADDR=:8080
ENV WEB_DIST_DIR=/app/web
USER cms
EXPOSE 8080
ENTRYPOINT ["cms"]
CMD ["serve"]
