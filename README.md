# johnny

The `johnny` command helps to maintain a GitHub account.

```
Usage: johnny <command> [<args>]

Options:
  --help, -h         display this help and exit

Commands:
  stale-prs          list the open PRs whose closing issues are already all closed.
  multiple-prs       list the open issues with multiple open closing PRs.
```

## Status

Beta software, but usable.

## Install

- Ensure you have `$HOME/go/bin` in your $PATH.
- No need to clone this repo.
- To install the latest release: `go install github.com/marco-m/johnny@latest`
- To install from the current `master` branch: `go install github.com/marco-m/johnny@master`

## Authentication

Requires a GitHub token passed as environment variable `GITHUB_TOKEN`.

We suggest to create one or more dedicated tokens, with the absolute minimum permissions to perform the wanted operation.
