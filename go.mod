module github.com/fclairamb/ftpserverlib

go 1.21

toolchain go1.23.2

require (
	github.com/fclairamb/go-log v0.5.0
	github.com/go-kit/log v0.2.1
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.11.0
	github.com/stretchr/testify v1.10.0
	golang.org/x/sys v0.26.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
