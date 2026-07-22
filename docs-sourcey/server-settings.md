# Server settings

`Settings` controls listener and protocol behavior. The most important groups
are:

- **Network:** `Listener`, `ListenAddr`, `PublicHost`, and `PublicIPResolver`.
- **Data channels:** passive port allocation, passive multiplexing, active-mode
  policy, and active/passive connection checks.
- **Timeouts:** idle and connection timeouts.
- **TLS:** `TLSRequired`, paired with `MainDriver.GetTLSConfig`.
- **Capabilities:** flags for MLSD, MLST, MFMT, LIST arguments, SITE, HASH,
  COMB, STAT, SYST, and active mode.
- **Presentation:** server banner and default transfer type.

Choose explicit connection-check policies when the server is reachable from
untrusted networks. Active FTP asks the server to connect outward, while
passive FTP exposes a server-side data listener; both deserve deliberate
address and port controls.

Consult the generated `Settings` reference before upgrading, because fields
and defaults may evolve with the library.
