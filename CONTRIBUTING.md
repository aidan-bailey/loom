# Contributing

Thank you for considering contributing to Loom! This document outlines the process for contributing.

## Development Setup

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/loom.git`
3. Add the upstream repository: `git remote add upstream https://github.com/aidan-bailey/loom.git`
4. Install dependencies: `go mod download`

### Dev loop

Run your build through the dev sandbox rather than directly — especially from inside loom, where a bare `./loom` is refused:

```bash
go run ./tools/loomdev up      # create + build a sandbox named after your branch
go run ./tools/loomdev run     # try it interactively
go run ./tools/loomdev down    # clean up
```

`go test -tags e2e ./e2e/...` runs the end-to-end smoke suite (requires tmux).

## Code Standards

### Lint

You can run the following command to lint the code:

```bash
gofmt -w .
```

### Testing

Please include tests for new features or bug fixes.

### Sign-off

Commits should be signed off with `git commit -s`, certifying the DCO in the commit trailer.

## Questions?

Feel free to open an issue for any questions about contributing.
