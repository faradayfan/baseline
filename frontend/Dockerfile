# Frontend image: build the Vite SPA, serve it with nginx, proxy /api -> backend.
# Build context is the frontend/ directory.
#
#   docker build -t baseline-ui:dev frontend/
#
# BACKEND_URL (e.g. http://RELEASE-baseline:8080) is substituted into the nginx
# conf at startup so one image works across environments.

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml* ./
RUN pnpm install --no-frozen-lockfile
COPY . .
RUN pnpm build

FROM nginx:1.27-alpine
# Template the nginx conf; the entrypoint envsubst's ${BACKEND_URL} at start.
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
ENV BACKEND_URL="http://baseline-baseline:8080"
EXPOSE 80
# nginx:alpine's entrypoint already runs envsubst over /etc/nginx/templates/*.
