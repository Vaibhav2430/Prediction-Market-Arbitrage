# Prediction-Market-Arbitrage
Real-time cross-platform arbitrage detection engine for Polymarket and Kalshi, built with Go and WebSockets.


## Initial read-only Go scanner

The current backend fetches one bounded page of active, open Polymarket
markets from the public Gamma API. It requires no credentials, places no
orders, and does not yet match contracts or detect executable arbitrage.
Kalshi public market discovery and order books are available with `-venue kalshi`.

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


## Kalshi public markets and order books

```sh
# One page of open binary markets, excluding multivariate combos:
go run ./cmd/scanner -venue kalshi -limit 3
# Filter discovery to a series:
go run ./cmd/scanner -venue kalshi -series KXHIGHNY -limit 3
# Inspect one verified active $1 binary market (use an actual current ticker):
go run ./cmd/scanner -venue kalshi -ticker YOUR-MARKET-TICKER -depth 5
# Use the exact Next cursor printed by discovery to fetch the next page:
go run ./cmd/scanner -venue kalshi -limit 3 -cursor 'CURSOR_FROM_OUTPUT'
```

The client uses the unauthenticated production REST API at
`https://external-api.kalshi.com/trade-api/v2`. It requests `status=open`;
returned open markets normally report `status=active`. Discovery retains
contract subtitles, closing time, and primary/secondary settlement rules for
future market matching. Contracts with a non-$1 notional or invalid metadata
produce warnings. Closed and nonbinary contracts are omitted. A direct ticker
lookup verifies contract metadata before requesting its order book.

Current `orderbook_fp.yes_dollars` and `no_dollars` contain bids only. The
client derives YES asks from NO bids and NO asks from YES bids at **$1 minus
the opposite bid**, preserving its contract quantity. For example, a NO bid
of $0.3500 for 12.50 contracts implies a YES ask of $0.6500 for 12.50 contracts.
Missing sides remain unavailable; an empty book is not a zero-price quote.
Kalshi can return an empty book for an unknown ticker, so book data alone is
not proof that a contract exists.

Prices use exact integer units of $0.0001 and quantities use hundredths of a
contract; floating point is not used for parsing or complements. Malformed,
negative, overflowing, over-precision, duplicate, or crossed book levels are
rejected. Levels are sorted best bid first. `-depth` selects 0 (all) or 1..100
levels per side; the CLI displays the best bid and derived ask with quantities.
The client retains all requested levels for later analysis.

Every request has a 10-second timeout and 4 MiB response cap. `-timeout` bounds
the entire scan; discovery performs one request plus one sequential order-book
request per retained market. Ctrl-C cancels work. No automatic retries or
unbounded pagination are performed. Invalid records warn; book failures warn
and cause a nonzero exit after valid books have been displayed. HTTP errors,
invalid response envelopes, and deadlines fail the run.

Quotes are snapshots, may be cached, exclude fees, and do not guarantee a fill.
The displayed receipt time is local, not an exchange timestamp. Polymarket
continues to show indicative Gamma prices; this update does not yet implement
Polymarket CLOB order books, cross-platform matching, or arbitrage detection.
No credentials, account operations, or trading are used.

API references: [public market data](https://docs.kalshi.com/getting_started/quick_start_market_data),
[markets](https://docs.kalshi.com/api-reference/market/get-markets), and
[order books](https://docs.kalshi.com/api-reference/market/get-market-orderbook).
