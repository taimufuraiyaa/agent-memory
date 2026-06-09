# Contributing to agent-memory

Thank you for your interest in contributing to agent-memory! This document provides guidelines and instructions for setting up your development environment and contributing to the project.

## Table of Contents

- [Development Environment Setup](#development-environment-setup)
- [Project Structure](#project-structure)
- [Development Workflow](#development-workflow)
- [Testing](#testing)
- [Code Style](#code-style)
- [Submitting Changes](#submitting-changes)

## Development Environment Setup

### Prerequisites

- Go 1.26.3 or later
- Node.js 20.x (for dashboard development)
- Python 3.11+ (for benchmarking)
- Git

### Option 1: VS Code Dev Container (Recommended)

The easiest way to get started is using the provided Dev Container:

1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop)
2. Install [VS Code](https://code.visualstudio.com/)
3. Install the [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)
4. Open this repository in VS Code
5. When prompted, click "Reopen in Container" (or use Command Palette: "Dev Containers: Reopen in Container")

The container will automatically:
- Install Go 1.26, Node.js 20, and Python 3.11
- Run `make setup` to install development tools
- Mount your `~/.agent-memory` directory for persistent data
- Configure VS Code with recommended extensions and settings

### Option 2: Docker Compose

For a lighter-weight containerized environment:

```bash
# Start development container
docker-compose up -d dev

# Enter the container
docker-compose exec dev bash

# Run tests in container
docker-compose run --rm test
```

### Option 3: Local Setup

1. **Install Go 1.26.3+**
   ```bash
   # Download from https://go.dev/dl/
   # or use version manager like asdf:
   asdf install golang 1.26.3
   ```

2. **Install Node.js 20.x**
   ```bash
   # Download from https://nodejs.org/
   # or use asdf:
   asdf install nodejs 20.11.0
   ```

3. **Install Python 3.11+**
   ```bash
   # Use your system package manager or asdf:
   asdf install python 3.11.7
   ```

4. **Clone and Setup**
   ```bash
   git clone https://github.com/taimufuraiyaa/agent-memory.git
   cd agent-memory
   make setup
   ```

### Using asdf Version Manager

If you use [asdf](https://asdf-vm.com/), this repository includes a `.tool-versions` file:

```bash
asdf plugin add golang
asdf plugin add nodejs
asdf plugin add python
asdf install  # Installs versions from .tool-versions
```

## Project Structure

```
agent-memory/
├── cmd/agent-memory/       # CLI entry point
├── internal/               # Internal packages
│   ├── api/               # HTTP API and dashboard
│   ├── cli/               # CLI commands
│   ├── config/            # Configuration management
│   ├── core/              # Domain types
│   ├── embeddings/        # Embedding providers
│   ├── engine/            # Core retrieval/write logic
│   ├── storage/           # Persistence layer
│   └── workspace/         # Workspace management
├── benchmark/             # Python benchmarking suite
├── tools/                 # Additional tools
│   └── agent-memory/
│       └── dashboard/     # React dashboard
├── .kiro/                 # Kiro specs and config
│   ├── specs/            # Feature specifications
│   ├── steering/         # Agent guidance
│   └── hooks/            # Agent hooks
└── Formula/              # Homebrew formula

```

## Development Workflow

### Available Make Targets

Run `make help` to see all available targets:

```bash
make setup           # Install development dependencies
make build           # Build the binary
make install-dev     # Build and install locally
make test            # Run all tests
make test-verbose    # Run tests with verbose output
make test-coverage   # Generate coverage report
make bench           # Run benchmarks
make fmt             # Format code
make vet             # Run go vet
make lint            # Run golangci-lint
make clean           # Remove build artifacts
make clean-all       # Remove all artifacts including coverage
```

### Common Development Tasks

**Build and test locally:**
```bash
make build
make test
```

**Install for local use:**
```bash
make install-dev
agent-memory --help
```

**Run with local changes:**
```bash
go run ./cmd/agent-memory search --query "test"
```

**Format and lint before committing:**
```bash
make fmt
make vet
make lint
```

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run with race detector and verbose output
make test-verbose

# Run specific package tests
go test ./internal/engine/...

# Run specific test
go test ./internal/engine -run TestRecallAssembly

# Generate coverage report
make test-coverage
# Open coverage.html in browser
```

### Writing Tests

- Place test files next to the code they test (`*_test.go`)
- Use table-driven tests for multiple test cases
- Use subtests with `t.Run()` for better organization
- Mock external dependencies
- Test error cases thoroughly

Example:
```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "test", "result", false},
        {"empty input", "", "", true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := MyFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("wanted error %v, got %v", tt.wantErr, err)
            }
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Code Style

### Pre-commit Hooks

We use pre-commit hooks to maintain code quality. Install them to automatically run checks before each commit:

```bash
# Install pre-commit (if not already installed)
pip install pre-commit

# Install the git hooks
pre-commit install

# Run hooks manually on all files (optional)
pre-commit run --all-files
```

**Available hooks:**
- **generate-ide-rules** - Regenerates IDE rule files from template
- **go-fmt** - Formats Go code with `gofmt`
- **go-mod-tidy** - Tidies `go.mod` and `go.sum`
- **go-vet** - Runs `go vet` to catch common mistakes
- **go-test** - Runs fast tests (with `-short` flag)
- **check-yaml** - Validates YAML syntax
- **check-json** - Validates JSON syntax
- **check-merge-conflict** - Detects merge conflict markers
- **check-added-large-files** - Prevents committing large files (>500KB)
- **detect-private-key** - Prevents committing private keys
- **mixed-line-ending** - Ensures consistent line endings (LF)
- **markdownlint** - Lints and fixes Markdown files
- **trailing-whitespace** - Removes trailing whitespace
- **end-of-file-fixer** - Ensures files end with newline

**Bypass hooks (emergency only):**
```bash
git commit --no-verify -m "emergency fix"
```

### Go Code

- Follow standard Go conventions and idioms
- Use `gofmt` for formatting (run `make fmt`)
- Keep functions focused and small
- Add comments for exported functions and types
- Use meaningful variable and function names

### Error Handling

Always add context to errors:

```go
// ❌ Bad
if err != nil {
    return err
}

// ✅ Good
if err != nil {
    return fmt.Errorf("failed to load workspace %s: %w", name, err)
}
```

### Package Documentation

Each package should have a `doc.go` file with:
- Package purpose
- Key concepts
- Usage examples
- Important considerations

## Submitting Changes

### Before Submitting

1. **Ensure tests pass:**
   ```bash
   make test
   ```

2. **Format and lint code:**
   ```bash
   make fmt
   make vet
   make lint
   ```

3. **Update documentation** if you've changed functionality

4. **Add tests** for new features or bug fixes

5. **Update TASKS.md** to check off completed tasks

### Commit Messages

Use clear, descriptive commit messages:

```
Short summary (50 chars or less)

More detailed explanation if needed. Wrap at 72 characters.
Explain what changed and why, not how.

- Bullet points are fine
- Reference issues: Fixes #123
```

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Commit with clear messages
5. Push to your fork
6. Open a Pull Request with:
   - Clear description of changes
   - Reference to related issues
   - Screenshots for UI changes
   - Test results

### Review Process

- Maintainers will review your PR
- Address feedback by pushing new commits
- Once approved, maintainers will merge

## Getting Help

- Check existing issues and discussions
- Read the documentation in `docs/`
- Ask questions in GitHub Discussions
- Review `.kiro/specs/` for feature context

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on the code, not the person
- Help create a welcoming community

Thank you for contributing! 🎉
