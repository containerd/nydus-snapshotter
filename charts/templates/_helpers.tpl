{{/*
Expand the name of the chart.
*/}}
{{- define "nydus-snapshotter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "nydus-snapshotter.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "nydus-snapshotter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "nydus-snapshotter.labels" -}}
helm.sh/chart: {{ include "nydus-snapshotter.chart" . }}
{{ include "nydus-snapshotter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "nydus-snapshotter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nydus-snapshotter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "nydus-snapshotter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nydus-snapshotter.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the configmap
*/}}
{{- define "nydus-snapshotter.configmapName" -}}
{{- printf "%s-config" (include "nydus-snapshotter.fullname" .) }}
{{- end }}

{{/*
Create the name of the cluster role
*/}}
{{- define "nydus-snapshotter.clusterRoleName" -}}
{{- printf "%s-cluster-role" (include "nydus-snapshotter.fullname" .) }}
{{- end }}

{{/*
Create the name of the cluster role binding
*/}}
{{- define "nydus-snapshotter.clusterRoleBindingName" -}}
{{- printf "%s-cluster-role-binding" (include "nydus-snapshotter.fullname" .) }}
{{- end }}

{{/*
Container image
*/}}
{{- define "nydus-snapshotter.image" -}}
{{- printf "%s/%s:%s" .Values.image.repository .Values.snapshotter.daemonset.image.name .Values.snapshotter.daemonset.image.tag }}
{{- end }}

{{/*
Render snapshotter config.toml from either a raw values entry or a bundled
chart file, then apply a small set of targeted operational overrides.
*/}}
{{- define "nydus-snapshotter.snapshotterConfig" -}}
{{- $config := .Values.snapshotter.configuration | default "" -}}
{{- if eq (trim $config) "" -}}
{{- $path := .Values.snapshotter.configurationFile | default "files/snapshotter/config.toml" -}}
{{- $matches := .Files.Glob $path -}}
{{- if eq (len $matches) 0 -}}
{{- fail (printf "snapshotter.configurationFile %q does not exist in the chart package" $path) -}}
{{- end -}}
{{- $config = .Files.Get $path -}}
{{- end -}}
{{- if eq (trim $config) "" -}}
{{- fail "snapshotter configuration must not be empty" -}}
{{- end -}}

{{- if .Values.snapshotter.nydusdPath -}}
{{- $current := regexFind `(?m)^nydusd_path = ".*"$` $config -}}
{{- if eq $current "" -}}
{{- fail "snapshotter.nydusdPath requires the resolved snapshotter config to contain a nydusd_path entry" -}}
{{- end -}}
{{- $config = replace $current (printf "nydusd_path = %q" .Values.snapshotter.nydusdPath) $config -}}
{{- end -}}

{{- if .Values.snapshotter.logLevel -}}
{{- $current := regexFind `(?m)^level = ".*"$` $config -}}
{{- if eq $current "" -}}
{{- fail "snapshotter.logLevel requires the resolved snapshotter config to contain a log level entry" -}}
{{- end -}}
{{- $config = replace $current (printf "level = %q" .Values.snapshotter.logLevel) $config -}}
{{- end -}}

{{- $config -}}
{{- end }}
