# Tracker — Site Map

A human-readable index of every page on this site.

## Pages

{{ range where site.RegularPages "Section" "" }}
- [{{ .Title }}]({{ .Permalink }}) — {{ .Params.description | default .Summary | truncate 120 }}
{{ end }}

## Machine-readable

- [llms.txt]({{ "llms.txt" | absURL }}) — short orientation prompt for LLM agents.
- [sitemap.xml]({{ "sitemap.xml" | absURL }}) — XML sitemap.
- [robots.txt]({{ "robots.txt" | absURL }}) — crawl policy.
- [AGENTS.md]({{ "AGENTS.md" | absURL }}) — agent-readable site orientation.

## Source

- [GitHub repository]({{ site.Params.repoURL }})
