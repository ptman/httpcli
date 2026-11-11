# httpcli

`httpcli` sends the requests of a [`.http` file](https://http-files.org/), like
[httpyac](https://httpyac.github.io/) or the JetBrains/VS Code HTTP clients,
from the terminal. It is a single Go binary that uses only the standard
library.

## Build

```sh
go build -o httpcli .
# or
go install .
```

## Usage

```text
httpcli run <file.http> [flags]

  -all               run every request in the file
  -name <name>       run the request with this # @name (and its @refs)
  -line <number>     run the request at or before this line
  -env <name>        environment from http-client.env.json
  -var <name=value>  define a variable (repeatable)
  -output <format>   short (default), body, headers or none
  -timeout <dur>     timeout per request (default 30s)
```

Only the first request is sent unless `-all`, `-name` or `-line` selects more.
Flags go before the file name.

```sh
httpcli run testdata/basic.http
httpcli run -all -output none testdata/basic.http
httpcli run -name login -env dev -var token=abc api.http
```

Exit codes: `0` all selected requests and assertions succeeded, `1` a request
failed (transport error or failed assertion), `2` usage, parse or environment
error.

Default output shows the request title (when the region has one), the method,
the resolved URL, status, duration, size and the pretty-printed body:

```text
GET https://httpbin.org/get?name=httpcli
200 OK (312ms, 291 B)
{
  "args": {
    "name": "httpcli"
  },
  ...
}
```

Assertion failures are written to stderr and set the exit code to 1:

```text
assertion failed: line 5: status == 200: got 500
```

## File format

```http
@baseUrl = https://httpbin.org
@user = httpcli

### Fetch an echo
# @name echo
GET {{baseUrl}}/get?name={{user}}
Accept: application/json

?? status == 200
?? body url endsWith /get?name=httpcli

### Create something
POST {{baseUrl}}/post
Content-Type: application/json

{"name": "{{user}}"}
```

Supported syntax:

- `###` request separators with an optional title; `#`, `//` and `/* */`
  comments, including an inline `/* */` at the start of a line.
- Request line with an optional method (default `GET`) and an optional
  `HTTP/1.1` suffix; URLs continued on indented lines.
- Relative URLs: `GET /path` is resolved against a `host` variable
  (`@host = https://env.example.com`); without it only absolute http(s) URLs
  can be sent.
- Headers; values continued on indented lines.
- Bodies: JSON, XML, plain text, URL-encoded forms (continued with leading
  `&`), and file includes — `< file` (raw) and `<@ file` (variables
  substituted). Includes are relative to the `.http` file and confined to its
  directory: `../` paths are rejected.
- Variables: `@name = value` / `@name := value`, substituted as `{{name}}`,
  including dotted and array paths (`{{login.user.name}}`, `{{items.0.id}}`).
  `{{%name}}` percent-encodes the value for use in a URL: unreserved
  characters are kept and everything else is escaped, so the result is safe in
  a path segment and in a query value. `{{$uuid}}`, `{{$timestamp}}`,
  `{{$isoTimestamp}}` and `{{$randomInt}}` (with or without the `$`) are
  generated on each substitution.
- Response chaining: `# @name login` stores the JSON response (names must be
  unique); later requests
  can use `{{login.token}}`, `{{login.user.roles.0}}` or the VS Code REST
  Client spelling `{{login.response.body.$.token}}`. `# @ref login` runs the
  named request first even when only the current request is selected.
- `Authorization: oauth2 client_credentials` (or plain `oauth2`) with
  `@oauth2_tokenEndpoint`, `@oauth2_clientId`, `@oauth2_clientSecret` and an
  optional `@oauth2_scope`. Tokens are cached for the run.
- `Authorization: Basic user pass` and `Authorization: Basic user:pass` are
  base64-encoded automatically; an already-encoded `Basic <base64>` value is
  sent unchanged. Only `Authorization` and `Proxy-Authorization` are rewritten.

Not supported: JavaScript blocks and response handlers (`{{ ... }}`,
`{% ... %}`), GraphQL/WebSocket/gRPC requests, and redirect control.

## Assertions

One `??` line per assertion, after the headers or body:

```text
?? status == 200
?? status >= 200
?? header Content-Type includes application/json
?? body json.name == httpcli
?? body includes "count": 3
?? body url endsWith /post
?? duration < 5000
```

| Subject    | Field                                                            |
| ---------- | ---------------------------------------------------------------- |
| `status`   | HTTP status code                                                 |
| `header`   | response header, e.g. `?? header Content-Type == application/json` |
| `body`     | JSON path (`a.b.0`) or, without a path, the raw body text        |
| `duration` | round-trip time in milliseconds                                  |

Conditions: `==`/`equals`, `!=`, `>`, `>=`, `<`, `<=`, `startsWith`,
`endsWith`, `includes`/`contains`, `matches` (regular expression), and the
value-less `exists`, `isTrue`, `isFalse`, `isNumber`, `isBoolean`, `isString`,
`isArray`.

## Variables

Sources, lowest precedence first:

1. `.env` next to the file (`NAME=value`, `export`, quotes and trailing
   `# comments` accepted).
2. `http-client.env.json` next to the file: `$shared` always applies, `-env`
   selects one more block.
3. The process environment, so a shell variable can retarget a run without
   editing files.
4. `-var name=value` on the command line.
5. File-level `@name = value`, then request-level ones.

Named responses are added while the run progresses and are visible to later
requests only.

## Alternatives

Other implementations of the same file format, for editor integration or
features this tool leaves out:

| Tool | Form | Notes |
| --- | --- | --- |
| [httpyac](https://httpyac.github.io/) | CLI and VS Code extension (Node.js) | Closest equivalent: JetBrains and REST Client syntax, JavaScript scripting, gRPC/WebSocket/GraphQL, `--json` and `--junit` output. |
| [VS Code REST Client](https://marketplace.visualstudio.com/items?itemName=humao.rest-client) | VS Code extension | The original `.http` client (2017); variables and environments, no scripting. |
| [JetBrains HTTP Client](https://www.jetbrains.com/help/idea/http-client-in-product-code-editor.html) | Built into JetBrains IDEs | Pre-request scripts, response handlers, and the `http-client.env.json` format this tool follows. |
| [ijhttp](https://www.jetbrains.com/help/idea/http-client-cli.html) | CLI (JDK 21+, Docker) | JetBrains' headless runner for CI pipelines. |
| [kulala.nvim](https://github.com/mistweaverco/kulala.nvim) | Neovim plugin and CLI | Aims for full JetBrains compatibility, with Lua and JavaScript response handlers. |
| [Visual Studio 2022](https://learn.microsoft.com/en-us/aspnet/core/test/http-files) | Built into the IDE | Uses the JetBrains environment format plus secret providers such as Azure Key Vault. |
| [httprunner](https://github.com/christianhelle/httprunner) | Rust CLI, TUI and GUI | Reads both JetBrains and REST Client syntax; HTML and Markdown reports. |

A maintained directory of clients, CLIs and parsers is at
[http-files.org](https://http-files.org/clients/overview).

## Development

Run `make` for the target list; the targets are:

| Target               | What it does                                              |
| -------------------- | --------------------------------------------------------- |
| `make build`         | builds `./httpcli`                                        |
| `make test`          | runs all tests (offline)                                  |
| `make coverage`      | writes `coverage.out`/`coverage.html` and prints the total |
| `make gofumpt`       | formats the tree with gofumpt                             |
| `make vet`           | `go vet ./...`                                            |
| `make golangci-lint` | runs golangci-lint                                        |
| `make govulncheck`   | checks dependencies for known vulnerabilities             |
| `make tools`         | installs the pinned gofumpt, golangci-lint, govulncheck   |
| `make clean`         | removes `httpcli` and coverage output                     |

`make build` produces a static (`CGO_ENABLED=0`), stripped (`-s -w`) binary;
run `make build LDFLAGS=` for a debuggable one.

Tool versions are pinned in the `Makefile`; `make tools` installs them into
`GOBIN`. CI (`.github/workflows/ci.yml`) runs build, test, coverage, vet, a
gofumpt check, golangci-lint and govulncheck on every push and pull request.

`testdata/` holds requests with assertions against a live httpbin. The base
URL comes from `HTTPBIN_BASE_URL`, defaulting to `https://httpbin.org` (set in
`testdata/.env`):

```sh
# default instance: https://httpbin.org
go run . run -all testdata/basic.http

# override with a local or alternate instance
HTTPBIN_BASE_URL=http://localhost:8080 go run . run -all testdata/basic.http

# run every testdata file through the assertions
HTTPCLI_LIVE=1 go test ./internal/cli -run TestLiveHTTPBin -v
HTTPBIN_BASE_URL=http://localhost:8080 HTTPCLI_LIVE=1 go test ./internal/cli -run TestLiveHTTPBin -v
```

The live test is skipped unless `HTTPCLI_LIVE=1` is set, so the normal test
suite stays offline and deterministic. It runs one file per second because
`httpbin.org` rate-limits bursts with HTTP 429.

## License

ISC — see [LICENSE](LICENSE).
