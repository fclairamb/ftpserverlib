module github.com/fclairamb/ftpserverlib

go 1.16

require (
	github.com/fclairamb/go-log v0.1.0
	github.com/go-kit/kit v0.11.0
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.7.0
	github.com/stretchr/testify v1.7.0
	golang.org/x/sys v0.0.0-20211216021012-1d35b9e2eb4e
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
