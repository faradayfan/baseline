{{/* Chart name, truncated to 63 chars. */}}
{{- define "baseline.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name. */}}
{{- define "baseline.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "baseline.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "baseline.labels" -}}
app.kubernetes.io/part-of: {{ include "baseline.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{/*
Build an image reference. A bare repo (e.g. "baseline") gets the configured
registry prefixed; a repo that already names a registry host (a "." or ":" in
its first path segment, e.g. pgvector/pgvector or gcr.io/...) is left untouched.
Usage: {{ include "baseline.image" (dict "image" .Values.image "global" .Values.image "chart" .Chart) }}
*/}}
{{- define "baseline.image" -}}
{{- $registry := .global.registry -}}
{{- $repo := .image.repository -}}
{{- $tag := default .global.tag (.image.tag | default "") | default .chart.AppVersion -}}
{{- $firstSeg := first (splitList "/" $repo) -}}
{{- if and $registry (not (contains "." $firstSeg)) (not (contains ":" $firstSeg)) -}}
{{- printf "%s/%s:%s" $registry $repo $tag -}}
{{- else -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end -}}
{{- end -}}

{{/* In-cluster Postgres service hostname. */}}
{{- define "baseline.postgresHost" -}}
{{- printf "%s-postgres" .Release.Name -}}
{{- end -}}

{{/*
Postgres DSN for the app, built from the Bitnami subchart's auth.* values + the
in-cluster host (the subchart service is <release>-postgres via the "postgres" alias).
Usage: {{ include "baseline.postgresDSN" . }}
*/}}
{{- define "baseline.postgresDSN" -}}
{{- $host := include "baseline.postgresHost" . -}}
{{- $auth := .Values.postgres.auth -}}
{{- printf "postgres://%s:%s@%s:5432/%s?sslmode=disable" $auth.username $auth.password $host $auth.database -}}
{{- end -}}

{{/* Name of the Secret holding DATABASE_URL (honors an external Secret). */}}
{{- define "baseline.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" .Release.Name -}}
{{- end -}}
{{- end -}}

{{/*
Embedder env vars (EMBEDDER_URL/MODEL/DIMS) for Baseline's own fact embedder.
Emitted only when embedder.url is set — empty url → no env → server runs with a
nil embedder (substring search, NULL fact embeddings). Shared by the deployment
(write+read paths) and the reaper cronjob (the embed-backfill mode runs there).
Usage: {{- include "baseline.embedderEnv" . | nindent 12 }}
*/}}
{{- define "baseline.embedderEnv" -}}
{{- with .Values.embedder -}}
{{- if .url }}
- name: EMBEDDER_URL
  value: {{ .url | quote }}
- name: EMBEDDER_MODEL
  value: {{ .model | default "nomic-embed-text" | quote }}
- name: EMBEDDER_DIMS
  value: {{ .dims | default 768 | quote }}
{{- end -}}
{{- end -}}
{{- end -}}
