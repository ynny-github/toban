---
tracker:
  kind: github
  github:
    project: https://github.com/users/ynny-github/projects/3
    label: ready-for-agent
    assignee: ynny-github
executor:
  kind: orca
max_concurrent: 2
---
{{.Issue.Repo}}#{{.Issue.Number}} 「{{.Issue.Title}}」に取り組む。

{{.Issue.Body}}
