module github.com/fclairamb/ftpserverlib

go 1.26.0

toolchain go1.27.1

require (
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.15.0
	github.com/stretchr/testify v1.12.1
	golang.org/x/sys v0.48.0
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/text v0.28.0 // indirect
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
