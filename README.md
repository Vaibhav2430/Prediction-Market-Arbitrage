# Prediction-Market-Arbitrage
Real-time cross-platform arbitrage detection engine for Polymarket and Kalshi, built with Go and WebSockets.


## Initial read-only Go scanner

The current backend fetches one bounded page of active, open Polymarket
markets from the public Gamma API. It requires no credentials, places no
orders, and does not yet connect to Kalshi or detect executable arbitrage.

```sh
go run ./cmd/scanner -limit 20 -offset 0
# Next API page (offset counts API records, including omitted/invalid records):
go run ./cmd/scanner -limit 20 -offset 20 -timeout 30s
```

Use the Go version declared in `go.mod` (currently Go 1.27.4). There are no
third-party dependencies. Each HTTP request has a 10-second timeout; the
`-timeout` flag bounds the entire run. Ctrl-C cancels the request. HTTP errors
and invalid top-level responses fail the run. Invalid individual records
produce warnings on stderr while valid records remain available.

Prices are **indicative Gamma `outcomePrices`, not executable bid/ask quotes**.
They do not account for order-book depth, fees, slippage, or data freshness.
YES/NO labels are matched by name rather than array position. Missing prices
appear as `unavailable`; non-YES/NO markets are counted and omitted. A page
can therefore display fewer markets than its requested limit. No price-sum
or arbitrage calculation is performed.

Schema reference: [Polymarket list-markets documentation](https://docs.polymarket.com/api-reference/markets/list-markets).
The client requests `GET /markets?active=true&closed=false&limit=...&offset=...`,
checks returned status flags, and handles both JSON-encoded and native arrays
for `outcomes`, `outcomePrices`, and `clobTokenIds`. Prices can be numeric
strings or numbers; malformed, nonfinite, and out-of-range values are rejected.
The response body is bounded to 4 MiB.

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

Keep real credentials out of this repository. `.gitignore` excludes local
`.env` variants, credential files, secret directories, and private key files;
only dummy values belong in example environment files. The scanner does not
read any credential files or environment variables.
