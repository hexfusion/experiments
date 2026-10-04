{{- /* A model name as a Kubernetes object name: Qwen3-Coder-30B-A3B is qwen3-coder-30b-a3b. */ -}}
{{- define "frontdoor.slug" -}}
{{- regexReplaceAll "[^a-z0-9-]+" (lower .) "-" | trimAll "-" -}}
{{- end }}
