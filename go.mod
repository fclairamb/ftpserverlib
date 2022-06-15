module github.com/fclairamb/ftpserverlib

go 1.18

require (
	github.com/fclairamb/go-log v0.3.0
	github.com/go-kit/log v0.2.1
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.8.2
	github.com/stretchr/testify v1.7.2
	golang.org/x/sys v0.0.0-20220615213510-4f61da869c0c
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/text v0.3.7 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
