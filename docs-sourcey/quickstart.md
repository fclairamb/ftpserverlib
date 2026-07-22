# Quickstart

Install the module in a Go project:

```bash
go get github.com/fclairamb/ftpserverlib
```

An embedding application implements `MainDriver`. The driver returns server
settings, authenticates users, chooses a `ClientDriver`, and optionally
provides a TLS configuration. A `ClientDriver` is an `afero.Fs`, so in-memory,
OS-backed, or custom storage can sit behind the FTP protocol.

For the smallest complete working implementation, start with the test driver
in [`driver_test.go`](https://github.com/fclairamb/ftpserverlib/blob/main/driver_test.go).
The companion [`ftpserver`](https://github.com/fclairamb/ftpserver) repository
is the easiest way to exercise the library before embedding it.

Next, read [Driver contract](driver-contract) and [Server settings](server-settings),
then use the generated [Go API](../api) for exact signatures.
