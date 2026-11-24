# Prediction-Market-Arbitrage
Real-time cross-platform arbitrage detection engine for Polymarket and Kalshi, built with Go and WebSockets.


## Read-only Go scanner

The backend discovers active Polymarket markets through Gamma and fetches
public CLOB order books for their YES and NO assets. Kalshi market discovery
and order books are available with `-venue kalshi`. No credentials are needed.
The scanner does not place orders or detect arbitrage. A separate matcher
ranks potential contract pairs for manual review; it never verifies equivalence.

```sh
go run ./cmd/scanner -limit 3 -offset 0
# Next API page (offset counts API records, including omitted/invalid records):
go run ./cmd/scanner -limit 3 -offset 3 -timeout 30s
# Original Gamma-only view (no order-book requests):
go run ./cmd/scanner -indicative -limit 20
```

Use the Go version declared in `go.mod` (currently Go 1.27.4). There are no
third-party dependencies. Each HTTP request has a 10-second timeout; the
`-timeout` flag bounds the entire run. Ctrl-C cancels the request. HTTP errors
and invalid top-level responses fail the run. Invalid individual records
produce warnings on stderr while valid records remain available.

With `-indicative`, prices are **Gamma `outcomePrices`, not executable bid/ask quotes**.
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
uses actual CLOB bid/ask snapshots by default, with `-indicative` available for
Gamma-only output. Cross-platform matching and arbitrage detection remain
unimplemented.
No credentials, account operations, or trading are used.

API references: [public market data](https://docs.kalshi.com/getting_started/quick_start_market_data),
[markets](https://docs.kalshi.com/api-reference/market/get-markets), and
[order books](https://docs.kalshi.com/api-reference/market/get-market-orderbook).


## Polymarket CLOB order books

```sh
# Default: discover a page, then fetch the actual YES and NO books:
go run ./cmd/scanner -venue polymarket -limit 3
# Gamma-only indicative prices, preserving the earlier scanner mode:
go run ./cmd/scanner -venue polymarket -indicative -limit 20
```

The scanner matches YES/NO by outcome label and requests `GET /book?token_id=...`
from `https://clob.polymarket.com` for each asset. It uses `clobTokenIds` for CTF
markets and `positionIds` for markets marked `version=v2`. Asset IDs and market
condition IDs are checked against Gamma; missing, duplicated, or mismatched
identifiers fail the affected market with a warning. One-sided and empty books
remain unavailable on their missing sides. No quotes are inferred from Gamma
prices or from the opposite outcome's book.

Prices are exact $0.0001 units and share sizes are exact millionths of a share.
Excess significant decimal precision, negatives, nonfinite values, overflow,
duplicate levels, off-tick prices, and crossed bid/ask books are rejected.
Trailing decimal zeroes are accepted without rounding. Bid levels are sorted
highest first and asks lowest first, independent of server ordering.

The CLI displays the best bid and ask with their sizes, the API snapshot time,
local receipt time, snapshot age at receipt, tick size, and minimum order size.
A displayed level can be smaller than the minimum order size; it is not a
promise that an order of that size can be placed or filled. Quotes exclude
fees and can change. A snapshot timestamp is not a freshness guarantee, and
YES/NO reads are separate snapshots rather than an atomic pair. Full depth is
retained by the client; `-depth` continues to apply only to Kalshi requests.

Each request has a 10-second timeout and a 4 MiB response cap. The overall
`-timeout` covers discovery and both sequential book reads per market; use a
small `-limit` for quick snapshots. There are no automatic retries. Book HTTP
or validation failures warn, preserve any successful side's output, and result
in a nonzero exit with an incomplete-data summary. A missing book is never
replaced with an indicative price. Credentials and trading are not used.

API references: [CLOB order book](https://docs.polymarket.com/api-reference/market-data/get-order-book)
and [prices and order books](https://docs.polymarket.com/market-data/prices-order-books).


## Candidate market matching (manual review)

```sh
# Default: up to three pages of 100 API records from each platform:
go run ./cmd/matcher
# Focus the sampled metadata on a topic and show up to 10 candidate pairs:
go run ./cmd/matcher -pages 5 -query bitcoin -top 10
# Continue discovery from a known offset/cursor or filter Kalshi to a series:
go run ./cmd/matcher -polymarket-offset 300 -kalshi-series KXBTC
# Full original metadata and warnings in structured output:
go run ./cmd/matcher -format json > candidates.json
```

The matcher uses public metadata only: it does not fetch order books, compare
prices, calculate arbitrage, or trade. Every candidate has `status=needs_review`,
including identical titles or rules. There is no automatically verified state.
A shared YES/NO label does not establish that the YES predicates align.

Candidates are ranked by lexical Dice overlap, taking the higher overlap of
title words and title plus event/YES-label context. At least two distinct shared
non-numeric terms are required. Common grammatical words and generic market
terms are ignored. The default `-min-score` is 0.45; scores are text similarity,
not probabilities or confidence in equivalence. This heuristic can miss aliases,
synonyms, and short titles, or suggest unrelated contracts sharing generic words.
It does not understand full settlement-rule semantics.

The text report wraps original titles, event details, outcome labels, deadlines,
and full primary/secondary rules in side-by-side columns without truncation.
JSON preserves raw text. Polymarket's Gamma description supplies its rules;
Kalshi's primary/secondary rules are retained along with expected/latest expiry
and early-close conditions when provided. Missing fields display as unavailable.
Numeric differences, directional/negative wording, missing metadata, imprecise
or differing deadlines are flagged. Gamma `endDate` and Kalshi `close_time` are
explicitly distinguished: matching timestamps are not evidence that event
cutoffs or resolution windows are equivalent. Review sources, thresholds,
postponements, cancellations, and exceptional settlement outcomes in full.

Discovery is bounded to `-pages` (1..10) of `-page-size` (1..100) per venue.
Records are deduplicated by venue ID. Kalshi stops at an empty or repeated
cursor. Gamma uses fixed offsets; retained record counts do not imply a complete
API page because the client filters invalid records. Reports include scanned
counts and continuation offsets/cursors. `-query` is a case-insensitive local
substring filter on sampled metadata on both platforms, not a remote catalog
search. The platforms have different listing orders; default pages may have
no overlapping events. No results means no candidates in this sample, not no
matches or arbitrage in the full catalogs.

`-top` (1..50) bounds returned pairs and ranking memory. Requests inherit the
existing 10-second timeout and 4 MiB cap; `-timeout` (default 30s) covers discovery
and comparison. Ctrl-C cancels work. Individual malformed records warn; a failed
discovery request aborts without publishing a partial candidate report.
Existing `cmd/scanner` modes are unchanged. No credentials or trading are used.
