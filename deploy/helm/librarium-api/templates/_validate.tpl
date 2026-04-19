{{/*
Chart-wide validation. Runs before any resource is rendered so that
misconfigurations fail at `helm install`/`helm template` time rather than at
pod startup.

Two supported paths for JWT_SECRET:
  A. inline    — set .Values.secret.JWT_SECRET
  B. external  — leave .Values.secret empty and set .Values.env.JWT_SECRET
                 (typically a secretKeyRef into a SealedSecret/ExternalSecret).
*/}}
{{- define "librarium-api.validate" -}}
  {{- $secretMap := default dict .Values.secret -}}
  {{- $envMap := default dict .Values.env -}}
  {{- $inlineVal := get $secretMap "JWT_SECRET" -}}
  {{- $hasInline := ne (toString $inlineVal) "" -}}
  {{- $hasExternal := hasKey $envMap "JWT_SECRET" -}}

  {{- if not (or $hasInline $hasExternal) -}}
    {{- fail (printf "\nlibrarium-api: JWT_SECRET is not configured.\n\nChoose one of:\n  (A) inline:   set `secret.JWT_SECRET` to a generated value\n  (B) external: leave `secret: {}` and set `env.JWT_SECRET.valueFrom.secretKeyRef`\n                (pointing at a SealedSecret/ExternalSecret/SOPS Secret)\n\nGenerate a value with:  openssl rand -base64 48\nSee the chart README §Secrets for details.\n") -}}
  {{- end -}}

  {{- if eq (toString $inlineVal) "CHANGE_ME" -}}
    {{- fail "\nlibrarium-api: `secret.JWT_SECRET` is still set to the placeholder 'CHANGE_ME'.\nGenerate a real value:  openssl rand -base64 48\n" -}}
  {{- end -}}
{{- end -}}
