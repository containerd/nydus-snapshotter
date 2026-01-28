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
Default snapshotter config values used to render config.toml.
*/}}
{{- define "nydus-snapshotter.snapshotterConfigDefaults" -}}
version: 1
root: /var/lib/containerd/io.containerd.snapshotter.v1.nydus
address: /run/containerd-nydus/containerd-nydus-grpc.sock
uid: 0
gid: 0
daemon_mode: dedicated
cleanup_on_close: false
delegate_nydusd: true
system:
  enable: true
  address: /run/containerd-nydus/system.sock
  uid: 0
  gid: 0
  debug:
    daemon_cpu_profile_duration_secs: 5
    pprof_address: ""
daemon:
  nydusd_config: /etc/nydus/nydusd-config.json
  nydusd_path: /opt/nydus/bin/nydusd
  nydusimage_path: /opt/nydus/bin/nydus-image
  fs_driver: fusedev
  recover_policy: failover
  threads_number: 4
  log_rotation_size: 100
  failover_policy: resend
cgroup:
  enable: true
  memory_limit: ""
log:
  log_to_stdout: false
  level: info
  dir: ""
  log_rotation_compress: true
  log_rotation_local_time: true
  log_rotation_max_age: 7
  log_rotation_max_backups: 5
  log_rotation_max_size: 100
metrics:
  address: ":9110"
  hung_io_interval: 10s
  collect_interval: 1m
remote:
  convert_vpc_registry: false
  skip_ssl_verify: false
  mirrors_config:
    dir: ""
  auth:
    enable_kubeconfig_keychain: false
    kubeconfig_path: ""
    enable_cri_keychain: false
    image_service_address: ""
    enable_kubelet_credential_providers: false
    credential_provider_config: ""
    credential_provider_bin_dir: ""
    credential_renewal_interval: 0s
snapshot:
  enable_nydus_overlayfs: false
  nydus_overlayfs_path: nydus-overlayfs
  enable_kata_volume: false
  sync_remove: false
cache_manager:
  disable: false
  gc_period: 24h
  cache_dir: ""
image:
  public_key_file: ""
  validate_signature: false
experimental:
  enable_stargz: false
  enable_referrer_detect: false
  enable_index_detect: false
  enable_backend_source: false
  tarfs:
    enable_tarfs: false
    mount_tarfs_on_host: false
    tarfs_hint: false
    max_concurrent_proc: 0
    export_mode: ""
{{- end }}

{{/*
Backward compatible aliases for the original flat snapshotter.config values.
*/}}
{{- define "nydus-snapshotter.snapshotterLegacyConfig" -}}
{{- $raw := .Values.snapshotter.config | default dict -}}
{{- $legacy := dict -}}

{{- if hasKey $raw "daemonMode" -}}
{{- $_ := set $legacy "daemon_mode" (index $raw "daemonMode") -}}
{{- end -}}
{{- if hasKey $raw "cleanupOnClose" -}}
{{- $_ := set $legacy "cleanup_on_close" (index $raw "cleanupOnClose") -}}
{{- end -}}
{{- if hasKey $raw "delegateNydusd" -}}
{{- $_ := set $legacy "delegate_nydusd" (index $raw "delegateNydusd") -}}
{{- end -}}

{{- $system := dict -}}
{{- if hasKey $raw "systemEnable" -}}
{{- $_ := set $system "enable" (index $raw "systemEnable") -}}
{{- end -}}
{{- $systemDebug := dict -}}
{{- if hasKey $raw "pprofAddress" -}}
{{- $_ := set $systemDebug "pprof_address" (index $raw "pprofAddress") -}}
{{- end -}}
{{- if gt (len $systemDebug) 0 -}}
{{- $_ := set $system "debug" $systemDebug -}}
{{- end -}}
{{- if gt (len $system) 0 -}}
{{- $_ := set $legacy "system" $system -}}
{{- end -}}

{{- $daemon := dict -}}
{{- if hasKey $raw "nydusdConfigPath" -}}
{{- $_ := set $daemon "nydusd_config" (index $raw "nydusdConfigPath") -}}
{{- end -}}
{{- if hasKey $raw "nydusdPath" -}}
{{- $_ := set $daemon "nydusd_path" (index $raw "nydusdPath") -}}
{{- end -}}
{{- if hasKey $raw "nydusImagePath" -}}
{{- $_ := set $daemon "nydusimage_path" (index $raw "nydusImagePath") -}}
{{- end -}}
{{- if hasKey $raw "fsDriver" -}}
{{- $_ := set $daemon "fs_driver" (index $raw "fsDriver") -}}
{{- end -}}
{{- if hasKey $raw "recoverPolicy" -}}
{{- $_ := set $daemon "recover_policy" (index $raw "recoverPolicy") -}}
{{- end -}}
{{- if hasKey $raw "threadsNumber" -}}
{{- $_ := set $daemon "threads_number" (index $raw "threadsNumber") -}}
{{- end -}}
{{- if hasKey $raw "nydusdLogRotationSize" -}}
{{- $_ := set $daemon "log_rotation_size" (index $raw "nydusdLogRotationSize") -}}
{{- end -}}
{{- if gt (len $daemon) 0 -}}
{{- $_ := set $legacy "daemon" $daemon -}}
{{- end -}}

{{- $cgroup := dict -}}
{{- if hasKey $raw "cgroupEnable" -}}
{{- $_ := set $cgroup "enable" (index $raw "cgroupEnable") -}}
{{- end -}}
{{- if hasKey $raw "cgroupMemoryLimit" -}}
{{- $_ := set $cgroup "memory_limit" (index $raw "cgroupMemoryLimit") -}}
{{- end -}}
{{- if gt (len $cgroup) 0 -}}
{{- $_ := set $legacy "cgroup" $cgroup -}}
{{- end -}}

{{- $log := dict -}}
{{- if hasKey $raw "logToStdout" -}}
{{- $_ := set $log "log_to_stdout" (index $raw "logToStdout") -}}
{{- end -}}
{{- if hasKey $raw "logLevel" -}}
{{- $_ := set $log "level" (index $raw "logLevel") -}}
{{- end -}}
{{- if hasKey $raw "logRotationCompress" -}}
{{- $_ := set $log "log_rotation_compress" (index $raw "logRotationCompress") -}}
{{- end -}}
{{- if hasKey $raw "logRotationLocalTime" -}}
{{- $_ := set $log "log_rotation_local_time" (index $raw "logRotationLocalTime") -}}
{{- end -}}
{{- if hasKey $raw "logRotationMaxAge" -}}
{{- $_ := set $log "log_rotation_max_age" (index $raw "logRotationMaxAge") -}}
{{- end -}}
{{- if hasKey $raw "logRotationMaxBackups" -}}
{{- $_ := set $log "log_rotation_max_backups" (index $raw "logRotationMaxBackups") -}}
{{- end -}}
{{- if hasKey $raw "logRotationMaxSize" -}}
{{- $_ := set $log "log_rotation_max_size" (index $raw "logRotationMaxSize") -}}
{{- end -}}
{{- if gt (len $log) 0 -}}
{{- $_ := set $legacy "log" $log -}}
{{- end -}}

{{- $metrics := dict -}}
{{- if hasKey $raw "metricsAddress" -}}
{{- $_ := set $metrics "address" (index $raw "metricsAddress") -}}
{{- end -}}
{{- if gt (len $metrics) 0 -}}
{{- $_ := set $legacy "metrics" $metrics -}}
{{- end -}}

{{- $remote := dict -}}
{{- if hasKey $raw "convertVpcRegistry" -}}
{{- $_ := set $remote "convert_vpc_registry" (index $raw "convertVpcRegistry") -}}
{{- end -}}
{{- $auth := dict -}}
{{- if hasKey $raw "enableKubeconfigKeychain" -}}
{{- $_ := set $auth "enable_kubeconfig_keychain" (index $raw "enableKubeconfigKeychain") -}}
{{- end -}}
{{- if hasKey $raw "kubeconfigPath" -}}
{{- $_ := set $auth "kubeconfig_path" (index $raw "kubeconfigPath") -}}
{{- end -}}
{{- if hasKey $raw "enableCriKeychain" -}}
{{- $_ := set $auth "enable_cri_keychain" (index $raw "enableCriKeychain") -}}
{{- end -}}
{{- if gt (len $auth) 0 -}}
{{- $_ := set $remote "auth" $auth -}}
{{- end -}}
{{- if gt (len $remote) 0 -}}
{{- $_ := set $legacy "remote" $remote -}}
{{- end -}}

{{- $snapshot := dict -}}
{{- if hasKey $raw "enableNydusOverlayfs" -}}
{{- $_ := set $snapshot "enable_nydus_overlayfs" (index $raw "enableNydusOverlayfs") -}}
{{- end -}}
{{- if hasKey $raw "nydusOverlayfsPath" -}}
{{- $_ := set $snapshot "nydus_overlayfs_path" (index $raw "nydusOverlayfsPath") -}}
{{- end -}}
{{- if hasKey $raw "enableKataVolume" -}}
{{- $_ := set $snapshot "enable_kata_volume" (index $raw "enableKataVolume") -}}
{{- end -}}
{{- if hasKey $raw "syncRemove" -}}
{{- $_ := set $snapshot "sync_remove" (index $raw "syncRemove") -}}
{{- end -}}
{{- if gt (len $snapshot) 0 -}}
{{- $_ := set $legacy "snapshot" $snapshot -}}
{{- end -}}

{{- $cacheManager := dict -}}
{{- if hasKey $raw "cacheManagerDisable" -}}
{{- $_ := set $cacheManager "disable" (index $raw "cacheManagerDisable") -}}
{{- end -}}
{{- if hasKey $raw "cacheGcPeriod" -}}
{{- $_ := set $cacheManager "gc_period" (index $raw "cacheGcPeriod") -}}
{{- end -}}
{{- if hasKey $raw "cacheDir" -}}
{{- $_ := set $cacheManager "cache_dir" (index $raw "cacheDir") -}}
{{- end -}}
{{- if gt (len $cacheManager) 0 -}}
{{- $_ := set $legacy "cache_manager" $cacheManager -}}
{{- end -}}

{{- $image := dict -}}
{{- if hasKey $raw "publicKeyFile" -}}
{{- $_ := set $image "public_key_file" (index $raw "publicKeyFile") -}}
{{- end -}}
{{- if hasKey $raw "validateSignature" -}}
{{- $_ := set $image "validate_signature" (index $raw "validateSignature") -}}
{{- end -}}
{{- if gt (len $image) 0 -}}
{{- $_ := set $legacy "image" $image -}}
{{- end -}}

{{- $experimental := dict -}}
{{- if hasKey $raw "enableStargz" -}}
{{- $_ := set $experimental "enable_stargz" (index $raw "enableStargz") -}}
{{- end -}}
{{- if hasKey $raw "enableReferrerDetect" -}}
{{- $_ := set $experimental "enable_referrer_detect" (index $raw "enableReferrerDetect") -}}
{{- end -}}
{{- if hasKey $raw "enableBackendSource" -}}
{{- $_ := set $experimental "enable_backend_source" (index $raw "enableBackendSource") -}}
{{- end -}}
{{- $tarfs := dict -}}
{{- if hasKey $raw "enableTarfs" -}}
{{- $_ := set $tarfs "enable_tarfs" (index $raw "enableTarfs") -}}
{{- end -}}
{{- if hasKey $raw "mountTarfsOnHost" -}}
{{- $_ := set $tarfs "mount_tarfs_on_host" (index $raw "mountTarfsOnHost") -}}
{{- end -}}
{{- if hasKey $raw "tarfsHint" -}}
{{- $_ := set $tarfs "tarfs_hint" (index $raw "tarfsHint") -}}
{{- end -}}
{{- if hasKey $raw "tarfsMaxConcurrentProc" -}}
{{- $_ := set $tarfs "max_concurrent_proc" (index $raw "tarfsMaxConcurrentProc") -}}
{{- end -}}
{{- if hasKey $raw "tarfsExportMode" -}}
{{- $_ := set $tarfs "export_mode" (index $raw "tarfsExportMode") -}}
{{- end -}}
{{- if gt (len $tarfs) 0 -}}
{{- $_ := set $experimental "tarfs" $tarfs -}}
{{- end -}}
{{- if gt (len $experimental) 0 -}}
{{- $_ := set $legacy "experimental" $experimental -}}
{{- end -}}

{{- toYaml $legacy -}}
{{- end }}

{{/*
Merged snapshotter config used by the chart.
*/}}
{{- define "nydus-snapshotter.snapshotterConfigData" -}}
{{- $defaults := include "nydus-snapshotter.snapshotterConfigDefaults" . | fromYaml -}}
{{- $raw := .Values.snapshotter.config | default dict -}}
{{- $structured := omit $raw
  "daemonMode"
  "fsDriver"
  "recoverPolicy"
  "threadsNumber"
  "cleanupOnClose"
  "delegateNydusd"
  "systemEnable"
  "pprofAddress"
  "logToStdout"
  "logLevel"
  "logRotationCompress"
  "logRotationLocalTime"
  "logRotationMaxAge"
  "logRotationMaxBackups"
  "logRotationMaxSize"
  "metricsAddress"
  "nydusdConfigPath"
  "nydusdPath"
  "nydusImagePath"
  "nydusdLogRotationSize"
  "cgroupEnable"
  "cgroupMemoryLimit"
  "convertVpcRegistry"
  "enableKubeconfigKeychain"
  "kubeconfigPath"
  "enableCriKeychain"
  "enableNydusOverlayfs"
  "nydusOverlayfsPath"
  "enableKataVolume"
  "syncRemove"
  "cacheManagerDisable"
  "cacheGcPeriod"
  "cacheDir"
  "publicKeyFile"
  "validateSignature"
  "enableStargz"
  "enableReferrerDetect"
  "enableBackendSource"
  "enableTarfs"
  "mountTarfsOnHost"
  "tarfsHint"
  "tarfsMaxConcurrentProc"
  "tarfsExportMode"
-}}
{{- $legacy := include "nydus-snapshotter.snapshotterLegacyConfig" . | fromYaml | default dict -}}
{{- $config := mergeOverwrite (deepCopy $defaults) $structured $legacy -}}
{{- toYaml $config -}}
{{- end }}

{{/*
Render snapshotter config.toml, allowing either raw TOML or structured values.
*/}}
{{- define "nydus-snapshotter.snapshotterConfig" -}}
{{- if .Values.snapshotter.configuration -}}
{{- .Values.snapshotter.configuration -}}
{{- else -}}
{{- $config := include "nydus-snapshotter.snapshotterConfigData" . | fromYaml -}}
version = {{ $config.version }}
root = {{ $config.root | quote }}
address = {{ $config.address | quote }}
uid = {{ $config.uid }}
gid = {{ $config.gid }}
daemon_mode = {{ $config.daemon_mode | quote }}
cleanup_on_close = {{ $config.cleanup_on_close }}
delegate_nydusd = {{ $config.delegate_nydusd }}

[system]
enable = {{ $config.system.enable }}
address = {{ $config.system.address | quote }}
uid = {{ $config.system.uid }}
gid = {{ $config.system.gid }}

[system.debug]
daemon_cpu_profile_duration_secs = {{ $config.system.debug.daemon_cpu_profile_duration_secs }}
pprof_address = {{ $config.system.debug.pprof_address | quote }}

[daemon]
nydusd_config = {{ $config.daemon.nydusd_config | quote }}
nydusd_path = {{ $config.daemon.nydusd_path | quote }}
nydusimage_path = {{ $config.daemon.nydusimage_path | quote }}
fs_driver = {{ $config.daemon.fs_driver | quote }}
recover_policy = {{ $config.daemon.recover_policy | quote }}
threads_number = {{ $config.daemon.threads_number }}
log_rotation_size = {{ $config.daemon.log_rotation_size }}
failover_policy = {{ $config.daemon.failover_policy | quote }}

[cgroup]
enable = {{ $config.cgroup.enable }}
memory_limit = {{ $config.cgroup.memory_limit | quote }}

[log]
log_to_stdout = {{ $config.log.log_to_stdout }}
level = {{ $config.log.level | quote }}
dir = {{ $config.log.dir | quote }}
log_rotation_compress = {{ $config.log.log_rotation_compress }}
log_rotation_local_time = {{ $config.log.log_rotation_local_time }}
log_rotation_max_age = {{ $config.log.log_rotation_max_age }}
log_rotation_max_backups = {{ $config.log.log_rotation_max_backups }}
log_rotation_max_size = {{ $config.log.log_rotation_max_size }}

[metrics]
address = {{ $config.metrics.address | quote }}
hung_io_interval = {{ $config.metrics.hung_io_interval | quote }}
collect_interval = {{ $config.metrics.collect_interval | quote }}

[remote]
convert_vpc_registry = {{ $config.remote.convert_vpc_registry }}
skip_ssl_verify = {{ $config.remote.skip_ssl_verify }}

[remote.mirrors_config]
dir = {{ $config.remote.mirrors_config.dir | quote }}

[remote.auth]
enable_kubeconfig_keychain = {{ $config.remote.auth.enable_kubeconfig_keychain }}
kubeconfig_path = {{ $config.remote.auth.kubeconfig_path | quote }}
enable_cri_keychain = {{ $config.remote.auth.enable_cri_keychain }}
image_service_address = {{ $config.remote.auth.image_service_address | quote }}
enable_kubelet_credential_providers = {{ $config.remote.auth.enable_kubelet_credential_providers }}
credential_provider_config = {{ $config.remote.auth.credential_provider_config | quote }}
credential_provider_bin_dir = {{ $config.remote.auth.credential_provider_bin_dir | quote }}
credential_renewal_interval = {{ $config.remote.auth.credential_renewal_interval | quote }}

[snapshot]
enable_nydus_overlayfs = {{ $config.snapshot.enable_nydus_overlayfs }}
nydus_overlayfs_path = {{ $config.snapshot.nydus_overlayfs_path | quote }}
enable_kata_volume = {{ $config.snapshot.enable_kata_volume }}
sync_remove = {{ $config.snapshot.sync_remove }}

[cache_manager]
disable = {{ $config.cache_manager.disable }}
gc_period = {{ $config.cache_manager.gc_period | quote }}
cache_dir = {{ $config.cache_manager.cache_dir | quote }}

[image]
public_key_file = {{ $config.image.public_key_file | quote }}
validate_signature = {{ $config.image.validate_signature }}

[experimental]
enable_stargz = {{ $config.experimental.enable_stargz }}
enable_referrer_detect = {{ $config.experimental.enable_referrer_detect }}
enable_index_detect = {{ $config.experimental.enable_index_detect }}
enable_backend_source = {{ $config.experimental.enable_backend_source }}

[experimental.tarfs]
enable_tarfs = {{ $config.experimental.tarfs.enable_tarfs }}
mount_tarfs_on_host = {{ $config.experimental.tarfs.mount_tarfs_on_host }}
tarfs_hint = {{ $config.experimental.tarfs.tarfs_hint }}
max_concurrent_proc = {{ $config.experimental.tarfs.max_concurrent_proc }}
export_mode = {{ $config.experimental.tarfs.export_mode | quote }}
{{- end -}}
{{- end }}
