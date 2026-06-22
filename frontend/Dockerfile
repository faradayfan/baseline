# Frontend image: build the Vite SPA, serve it with nginx, proxy /api -> backend.
# Build context is the frontend/ directory.
#
#   docker build -t baseline-ui:dev frontend/
#
# BACKEND_URL (e.g. http://RELEASE-baseline:8080) is substituted into the nginx
# conf at startup so one image works across environments.

FROM node:26-alpine AS build
WORKDIR /app
# Node 26 dropped the bundled corepack, so install pnpm directly. Pinned to the
# version in frontend/.tool-versions to keep the build reproducible.
RUN npm install -g pnpm@11.5.3
# pnpm-workspace.yaml carries the esbuild build-script approval (allowBuilds);
# without it pnpm 11 fails install on the ignored-builds gate.
COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

# nginx:alpine (unversioned) tracks the freshest Alpine package rebuilds — it
# carries materially fewer CVEs than a minor-pinned tag (which lags on apk
# patches). The remaining unfixable findings are ignored with rationale in
# .grype.yaml. Rebuilds pull current packages; the grype gate catches drift.
FROM nginx:alpine
# Template the nginx conf; the entrypoint envsubst's ${BACKEND_URL} at start.
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
ENV BACKEND_URL="http://baseline-baseline:8080"
EXPOSE 80
# nginx:alpine's entrypoint already runs envsubst over /etc/nginx/templates/*.
