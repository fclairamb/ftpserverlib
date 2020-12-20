module github.com/fclairamb/ftpserverlib

go 1.14

require (
	github.com/go-kit/kit v0.10.0
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.5.1
	github.com/stretchr/testify v1.6.1
	golang.org/x/text v0.3.4 // indirect
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
