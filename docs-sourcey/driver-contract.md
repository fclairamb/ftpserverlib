# Driver contract

`MainDriver` is the integration boundary between the FTP engine and your
application. Implement it to:

- return a `Settings` value from `GetSettings`;
- produce the welcome message in `ClientConnected`;
- release per-client state in `ClientDisconnected`;
- authenticate credentials and return a `ClientDriver` from `AuthUser`;
- return the current `tls.Config` from `GetTLSConfig` when TLS is enabled.

`ClientDriver` embeds `afero.Fs`. This keeps ordinary filesystem operations in
a familiar interface and lets a server choose memory, disk, object-backed, or
application-specific storage.

Callbacks receive a `ClientContext`. It exposes the connection ID, addresses,
current path, TLS state, debug flag, last command, last data-channel mode, and
a method to close the connection. Treat it as connection-scoped state; do not
store it globally.
