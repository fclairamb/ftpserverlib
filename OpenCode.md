# OpenCode Configuration for ftpserverlib

## Build/Test/Lint Commands
- **Test all**: `go test ./...`
- **Test single**: `go test -run TestName -v`
- **Lint**: `golangci-lint run`
- **Format**: `gofmt -s -w .` or `goimports -w .`
- **Build**: `go build ./...`
- **Vet**: `go vet ./...`

## Code Style Guidelines
- **Package**: Single package `ftpserver` for the entire library
- **Imports**: Use `goimports` with local prefix `github.com/fclairamb/ftpserverlib`
- **Logging**: Use `github.com/fclairamb/go-log` with alias `log`
- **Error handling**: Use `errors.Is()` for error checking, wrap with context
- **Naming**: CamelCase for exported, camelCase for unexported
- **Line length**: Max 120 characters
- **Function length**: Max 80 lines, 40 statements
- **Interfaces**: Prefer small interfaces, use `afero.Fs` as base
- **Constants**: Group related constants with `iota`
- **Types**: Use type aliases for enums (e.g., `HASHAlgo int8`)
- **Testing**: Use `testify/assert` and `testify/require` and initialize instances with: `req := require.New(t)`
