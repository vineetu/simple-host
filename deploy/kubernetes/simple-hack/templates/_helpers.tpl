{{/*
Names and shared blocks. Every resource is named after the release so two
installs can share a namespace.
*/}}
{{- define "hack.name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hack.labels" -}}
app.kubernetes.io/name: simple-hack
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "hack.selectorLabels" -}}
app.kubernetes.io/name: simple-hack
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "hack.image" -}}
{{- $tag := .Values.image.tag | default (printf "hack-%s" .Chart.AppVersion) -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "hack.secretName" -}}
{{- .Values.secrets.existingSecret | default (printf "%s-secrets" (include "hack.name" .)) -}}
{{- end -}}

{{- define "hack.configName" -}}
{{- printf "%s-config" (include "hack.name" .) -}}
{{- end -}}

{{- define "hack.dataClaim" -}}
{{- .Values.persistence.existingClaim | default (printf "%s-data" (include "hack.name" .)) -}}
{{- end -}}

{{/* The Kubernetes "restricted" Pod Security Standard, pod and container halves. */}}
{{- define "hack.podSecurity" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
fsGroup: 65532
fsGroupChangePolicy: OnRootMismatch
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "hack.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}

{{/* Company sign-in is on when all three OIDC values are set. */}}
{{- define "hack.oidcOn" -}}
{{- if and .Values.signIn.oidc.issuer .Values.signIn.oidc.clientId (or .Values.signIn.oidc.clientSecret .Values.secrets.existingSecret) -}}true{{- end -}}
{{- end -}}

{{/*
What must be set before the pod can start. Checked once, here, so every
template fails with the same plain sentence.
*/}}
{{- define "hack.validate" -}}
{{- if not .Values.host }}
{{- fail "host is required: the platform's hostname, e.g. hack.example.com" }}
{{- end }}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$" .Values.host) }}
{{- fail (printf "host %q is not a lower-case hostname" .Values.host) }}
{{- end }}
{{- if and (not .Values.database.dsn) (not .Values.database.existingSecret) (not .Values.secrets.existingSecret) }}
{{- fail "database.dsn is required (or database.existingSecret, or secrets.existingSecret holding DB_DSN)" }}
{{- end }}
{{- if and .Values.database.dsn .Values.database.existingSecret }}
{{- fail "set database.dsn or database.existingSecret, not both" }}
{{- end }}
{{- range .Values.ingress.events }}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" .) }}
{{- fail (printf "ingress.events: %q is not an event slug (lower-case letters, digits, hyphens)" .) }}
{{- end }}
{{- end }}
{{- if not (has .Values.signIn.email.enabled (list "on" "off")) }}
{{- fail (printf "signIn.email.enabled must be \"on\" or \"off\", not %q" .Values.signIn.email.enabled) }}
{{- end }}
{{- $oidc := include "hack.oidcOn" . }}
{{- $partial := or .Values.signIn.oidc.issuer .Values.signIn.oidc.clientId .Values.signIn.oidc.clientSecret }}
{{- if and $partial (not $oidc) }}
{{- fail "company sign-in needs all of signIn.oidc.issuer, clientId and clientSecret (the secret may come from secrets.existingSecret)" }}
{{- end }}
{{- if and $oidc (hasPrefix "http://" .Values.signIn.oidc.issuer) (ne (index .Values.extraConfig "OIDC_INSECURE_ALLOWED" | default "" | toString) "true") }}
{{- fail "signIn.oidc.issuer must be https (a plain-http issuer is for tests, with extraConfig.OIDC_INSECURE_ALLOWED: \"true\")" }}
{{- end }}
{{- if and $oidc (eq .Values.signIn.oidc.issuer "https://accounts.google.com") (not .Values.signIn.oidc.allowedEmailDomains) }}
{{- fail "signIn.oidc.allowedEmailDomains is required when the issuer is Google, or any Google account could sign in" }}
{{- end }}
{{- $email := or .Values.signIn.email.resendApiKey .Values.secrets.existingSecret }}
{{- if and (not $oidc) (not $email) (not .Values.signIn.password.enabled) }}
{{- fail "nobody could sign in: enable signIn.password.enabled, set signIn.oidc (company sign-in) or signIn.email.resendApiKey and from (email codes)" }}
{{- end }}
{{- if and (eq .Values.signIn.email.enabled "off") (not $oidc) (not .Values.signIn.password.enabled) }}
{{- fail "signIn.email.enabled \"off\" needs company or password sign-in configured, or nobody could sign in" }}
{{- end }}
{{- if and .Values.signIn.email.resendApiKey (not .Values.signIn.email.from) }}
{{- fail "signIn.email.from is required with resendApiKey: the verified sender address" }}
{{- end }}
{{- if and .Values.secrets.existingSecret .Values.extraSecrets }}
{{- fail "extraSecrets go in secrets.existingSecret when that is set" }}
{{- end }}
{{- if and (not .Values.persistence.enabled) (not .Values.persistence.existingClaim) }}
{{- fail "persistence.enabled false loses every site on restart; set persistence.existingClaim instead if you made the claim yourself" }}
{{- end }}
{{- end -}}
