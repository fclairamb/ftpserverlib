# Changelog

## [0.32.0](https://github.com/fclairamb/ftpserverlib/compare/v0.31.0...v0.32.0) (2026-05-25)


### Features

* **settings:** add DisableASCIIConversion to skip CRLF/LF rewriting ([#636](https://github.com/fclairamb/ftpserverlib/issues/636)) ([5d53235](https://github.com/fclairamb/ftpserverlib/commit/5d5323575e1ebb478a3abb0cf8210f9a243b3d12)), closes [#615](https://github.com/fclairamb/ftpserverlib/issues/615)


### Bug Fixes

* **deps:** update module golang.org/x/sys to v0.45.0 ([#639](https://github.com/fclairamb/ftpserverlib/issues/639)) ([4b8e1bd](https://github.com/fclairamb/ftpserverlib/commit/4b8e1bdaade3b9f32cb77b6c8409c1eed4b113f9))

## [0.31.0](https://github.com/fclairamb/ftpserverlib/compare/v0.30.0...v0.31.0) (2026-05-16)


### Features

* **transfer:** add passive port multiplexing by client IP ([#623](https://github.com/fclairamb/ftpserverlib/issues/623)) ([feb3821](https://github.com/fclairamb/ftpserverlib/commit/feb38214dec56b42977515dc32491bcc39a5238a))


### Bug Fixes

* **deps:** update module golang.org/x/sys to v0.41.0 ([#611](https://github.com/fclairamb/ftpserverlib/issues/611)) ([5388ad1](https://github.com/fclairamb/ftpserverlib/commit/5388ad151705160cca3750ab1b4153c0046517fb))
* **deps:** update module golang.org/x/sys to v0.44.0 ([#620](https://github.com/fclairamb/ftpserverlib/issues/620)) ([c73a72e](https://github.com/fclairamb/ftpserverlib/commit/c73a72e78737007330a8a531d6a15d6a629802bd))

## [0.30.0](https://github.com/fclairamb/ftpserverlib/compare/v0.29.0...v0.30.0) (2026-01-24)


### Features

* make MODE Z configurable, disabled by default ([#604](https://github.com/fclairamb/ftpserverlib/issues/604)) ([23fbe72](https://github.com/fclairamb/ftpserverlib/commit/23fbe72645ceb8b6343a67761f8242aa221d5b29))


### Bug Fixes

* handle closed connection errors gracefully ([#602](https://github.com/fclairamb/ftpserverlib/issues/602)) ([21f789a](https://github.com/fclairamb/ftpserverlib/commit/21f789ac54d5c8119ea9cabc22159fba6b94a19a))

## [0.29.0](https://github.com/fclairamb/ftpserverlib/compare/v0.28.0...v0.29.0) (2026-01-11)


### Features

* **deflate:** Adding support for transfer compression ([#461](https://github.com/fclairamb/ftpserverlib/issues/461)) ([f90c6ac](https://github.com/fclairamb/ftpserverlib/commit/f90c6ac9ee1de783afc8a9cc5a935c5fba943f29))


### Bug Fixes

* **ci:** add release-please config files ([#600](https://github.com/fclairamb/ftpserverlib/issues/600)) ([cc8189b](https://github.com/fclairamb/ftpserverlib/commit/cc8189bcb6ff1a649dd794623ac4d2a11f89cae6))
* **deps:** update module golang.org/x/sys to v0.40.0 ([#597](https://github.com/fclairamb/ftpserverlib/issues/597)) ([3960a3a](https://github.com/fclairamb/ftpserverlib/commit/3960a3ad10e1c2a4a4a3a1126d6c654335302bcc))
