import { defineConfig, godoc, markdown } from "sourcey";

export default defineConfig({
  name: "ftpserverlib",
  description: "Go API reference for building custom FTP servers with ftpserverlib.",
  repo: "https://github.com/fclairamb/ftpserverlib",
  editBranch: "b4c3694ee73399d8a55293d568e5100c25e4d2d4",
  editBasePath: "docs-sourcey",
  navigation: {
    tabs: [
      {
        tab: "Guides",
        slug: "guides",
        source: markdown({
          groups: [
            {
              group: "Get started",
              pages: ["overview", "quickstart"],
            },
            {
              group: "Build a server",
              pages: ["driver-contract", "server-settings", "extensions"],
            },
          ],
        }),
      },
      {
        tab: "Go API",
        slug: "api",
        source: godoc({
          module: "..",
          packages: ["./..."],
          snapshot: "./godoc.json",
          mode: "snapshot",
          includeTests: true,
          sourceBasePath: "",
        }),
      },
    ],
  },
});
