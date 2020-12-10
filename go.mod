module github.com/fclairamb/ftpserverlib

go 1.14

require (
	github.com/go-kit/kit v0.10.0
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.5.1
	gopkg.in/dutchcoders/goftp.v1 v1.0.0-20170301105846-ed59a591ce14
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20200916091733-843d4cca4bb2
