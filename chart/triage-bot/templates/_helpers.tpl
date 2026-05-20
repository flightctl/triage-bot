{{/*
Expand the name of the chart.
*/}}
{{- define "triage-bot.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "triage-bot.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "triage-bot.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "triage-bot.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "triage-bot.selectorLabels" -}}
app.kubernetes.io/name: {{ include "triage-bot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "triage-bot.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "triage-bot.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name for API tokens
*/}}
{{- define "triage-bot.jiraSecretName" -}}
{{- if .Values.secrets.existingJiraSecret }}
{{- .Values.secrets.existingJiraSecret }}
{{- else }}
{{- include "triage-bot.fullname" . }}-api-tokens
{{- end }}
{{- end }}

{{/*
Vertex AI service account key secret name
*/}}
{{- define "triage-bot.vertexSecretName" -}}
{{- if .Values.secrets.existingVertexSecret }}
{{- .Values.secrets.existingVertexSecret }}
{{- else }}
{{- include "triage-bot.fullname" . }}-vertex
{{- end }}
{{- end }}
