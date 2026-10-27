# Development image: Go and the Tailwind standalone CLI.
FROM golang:1.25-alpine
RUN apk add --no-cache curl libstdc++ libgcc \
    && curl -fsSL -o /usr/local/bin/tailwindcss \
       https://github.com/tailwindlabs/tailwindcss/releases/download/v4.3.3/tailwindcss-linux-x64-musl \
    && chmod +x /usr/local/bin/tailwindcss
ARG UID=1000
ARG GID=1000
RUN addgroup -g ${GID} app && adduser -D -u ${UID} -G app app
USER app
RUN mkdir -p /home/app/go /home/app/.cache/go-build
ENV GOPATH=/home/app/go GOCACHE=/home/app/.cache/go-build
WORKDIR /app
EXPOSE 8000
CMD ["bin/dev"]
