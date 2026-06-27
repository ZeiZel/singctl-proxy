{{- define "singctl-license.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "singctl-license.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "singctl-license.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "singctl-license.labels" -}}
app.kubernetes.io/name: {{ include "singctl-license.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "singctl-license.selectorLabels" -}}
app.kubernetes.io/name: {{ include "singctl-license.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "singctl-license.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{ .Values.secret.existingSecret }}
{{- else -}}
{{ include "singctl-license.fullname" . }}
{{- end -}}
{{- end -}}
