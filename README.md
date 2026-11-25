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

After recognized structured conflicts are rejected, candidates are ranked by lexical Dice overlap, taking the higher overlap of
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
Recognized contract type, threshold, direction, observation, and source
conflicts reject pairs. Unparsed numeric/directional wording, missing metadata,
and imprecise or differing platform deadlines remain review flags. Gamma `endDate` and Kalshi `close_time` are
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


## Topic-focused discovery

```sh
# Search related Bitcoin events on both platforms (remote discovery):
go run ./cmd/matcher -topic bitcoin -top 5
# Use an exact Kalshi series rather than automatic series selection:
go run ./cmd/matcher -topic bitcoin -kalshi-series KXBTCD -top 5
# Other topics: provide Kalshi's exact, case-sensitive discovery category:
go run ./cmd/matcher -topic 'presidential election' -kalshi-category Politics
# Optionally narrow reported deadlines, using explicit timezone offsets:
go run ./cmd/matcher -topic bitcoin -deadline-after '2026-11-23T00:00:00-08:00' -deadline-before '2026-12-01T23:59:59-08:00'
# Structured review report:
go run ./cmd/matcher -topic bitcoin -format json > candidates.json
```

`-topic` changes discovery: Polymarket uses public text search and reads the
open markets nested in returned events. It validates each market's active/closed
flags because active events can still contain closed contracts. Event titles
are retained as matching context. `-topic-events` defaults to 5 events per page
(1..20); events can contain many markets. `-search-page` starts at 1 and follows
the API's `hasMore` flag, bounded by `-pages`.

Kalshi first fetches series metadata for one category, matches topic wording
against titles/tags locally, then requests open markets using each selected
`series_ticker`. Common crypto, economic, and sports topics infer a category;
other topics require `-kalshi-category` or `-kalshi-series`. This API does not
provide text search on its series-list endpoint. Title/tag matching can miss
aliases or synonyms. Automatic discovery ranks topic wording in titles, exact topic tags, modern
KX tickers, and broad price-series wording ahead of short tag-only matches;
it does not rank current liquidity or contract equivalence. It may include
series without open markets. Use an explicit series for precise targeting.

`-max-series` bounds automatic selection (default 5, maximum 10). Reports list
selected series and how many matched the topic, and warn about omitted series.
Each series has its own pagination cursor and up to `-pages` requests; cursors
are never reused across different series. With an explicit `-kalshi-series`,
`-kalshi-cursor` can resume that series. Otherwise use reported per-series
cursors only with the corresponding explicit series. Catalog mode's
`-polymarket-offset` is incompatible with topic search; use `-search-page`.

The optional inclusive `-deadline-after` and `-deadline-before` bounds filter
retrieved metadata locally. Both require RFC3339 timestamps with a timezone.
The report counts excluded contracts, including missing/imprecise dates. These
filters compare Gamma `endDate` and Kalshi `close_time`; they do not make event
cutoffs or settlement times equivalent. They are not remote full-calendar
filters. `-query` remains an additional local substring filter on fetched data.

Topic mode caps retained markets at 1000 per venue and keeps existing request
size/time limits and cancellation. A cap produces a warning; continuation may
skip unprocessed nested records on the capped page. Reduce `-topic-events` if
a search response exceeds 4 MiB. HTTP/validation failures stop the report rather
than silently falling back to unrelated catalogs. No related series or no
candidate pairs is a valid bounded search result, not proof of no overlap.
Every candidate remains `needs_review`. No prices, arbitrage, or trading are
introduced. Existing catalog matching and price scanner commands still work.

References: [Polymarket public search](https://docs.polymarket.com/api-reference/search/search-markets-events-and-profiles)
and [Kalshi series discovery](https://docs.kalshi.com/api-reference/market/get-series-list).


## Structured compatibility checks

The matcher now rejects recognized conflicts before returning ranked candidates.
The report counts text-similar pairs, rejected pairs, and rejection reasons;
one pair can have multiple reasons. Both JSON and text retain the surviving
candidates' parsed details, alongside original rules. Every survivor still has
`needs_review` status: absence of a recognized conflict is not equivalence.

Checks distinguish ranges from thresholds, compare both range boundaries,
compare exact amounts without floating point ($54,000 and $54k normalize to
54000), and preserve strict versus inclusive inequalities. Primary rules take
precedence over display labels when their predicate is recognized. Observation
mode (point versus touch), measurement (e.g. candle close versus index average),
known asset identity, source family, observation dates, and explicit clocks/time
zones are compared. No threshold tolerance, price rounding, or assumed mapping
between Binance and CF Benchmarks is used.

Observation dates and clocks come from title/rule wording, not Gamma endDate
or Kalshi close_time. Missing years are left unknown. Explicit ET clocks use
America/New_York timezone rules when a year/date is known; EST/EDT are fixed
stated offsets. Unknown or ambiguous details are disclosed for manual review.
The parser recognizes a limited set of English price predicates, assets,
sources, month/ISO dates, and explicit time zones. It is not a general natural
language settlement verifier, and unsupported wording can remain a candidate
with unknown details. Existing deadline-window filters remain discovery filters.

The previously shown range of $54,000-$56,000 on November 24 (Binance candle
close) versus above $51,100 on November 23 (CF Benchmarks average) is covered by
a regression test and is rejected rather than filling the top results. This
change does not introduce pricing comparisons, credentials, trading, automatic
approval, or a verified arbitrage claim.


## Explicit pair check (conditional pricing only)

```sh
# November CPI <=3.3% on Polymarket versus >3.3% on Kalshi:
# YES predicates are inverted in the ordinary on-time BLS release scenario.
go run ./cmd/paircheck -polymarket-id 7004029 -kalshi-ticker KXCPIYOY-26NOV-T3.3 -relation inverted -quantity 5
# Include complete current rules, books and fee metadata:
go run ./cmd/paircheck -polymarket-id 7004029 -kalshi-ticker KXCPIYOY-26NOV-T3.3 -relation inverted -quantity 5 -format json
```

This command does not establish equivalence. `-relation same` assumes that the
YES predicates align and compares Polymarket YES + Kalshi NO, and vice versa.
`-relation inverted` assumes opposite YES predicates and compares YES + YES
and NO + NO. The relation is a caller-supplied scenario, not an approval.
Every result remains unverified; a detected delay/cancellation policy mismatch
is explicitly marked incompatible. In particular, the CPI example can use a
previous-month fallback on Polymarket while Kalshi extends settlement during a
government shutdown. Its normal-release comparison is not a guaranteed hedge.
The Fed December no-change candidates also have different cancellation handling.

Metadata is checked first, then both Polymarket token books and the Kalshi
book are fetched concurrently. These are separate snapshots. Whole quantities
are limited to available best-ask sizes and Polymarket's minimum order size;
the tool does not consume deeper levels. Missing liquidity, unsupported fees,
or stale Polymarket timestamps do not produce a net comparison. The default
`-max-age` is 60s; Kalshi's REST book supplies no exchange timestamp, so receipt
time alone cannot establish its freshness. The default overall timeout is 30s.

Fees use public per-market Polymarket parameters and Kalshi series parameters
with event overrides. Unsupported/missing fee metadata withholds net results;
no zero-fee assumption or rebate is invented. The supported taker formula is
quantity * rate * price * (1-price). Kalshi's rate is 0.07 times the published
multiplier. Fee estimates round upward per aggregate leg to $0.00001 for
Polymarket and $0.0001 for Kalshi; actual fill splitting/rounding may differ.
Calculations use exact rationals. Funding, withdrawals, intermediary and
currency-conversion costs are excluded.

A conditional payout-minus-cost result assumes both legs fill and the stated
relation holds through settlement. It is never labeled guaranteed profit or
verified arbitrage, even when positive. All operations are public, read-only,
and unauthenticated; no orders are submitted.

Sources: [Polymarket fee formula and precision](https://docs.polymarket.com/trading/fees),
[Kalshi current fee schedule](https://kalshi.com/fee-schedule),
[Kalshi fee formula PDF](https://kalshi.com/docs/kalshi-fee-schedule.pdf), and
[Kalshi CPI contract terms](https://assets.kalshi.com/contract_terms/YOYCPI.pdf).


## Reviewed-pair registry

`data/reviewed-pairs.json` is a versioned, manually reviewed shortlist. It records
contract IDs, same/inverted YES mapping, original rule snapshots, source and
binding-term identities, SHA-256 fingerprints, reviewer/evidence fields, and
unresolved findings. Current entries are research records: the CPI pair is
rejected and the Newsom nomination pair needs further timing/exception review.
No entry is silently promoted to approved because titles or predicates match.

```sh
# Refresh current rules for all registry entries:
go run ./cmd/reviewed
# Refresh one entry:
go run ./cmd/reviewed -pair-id newsom-democratic-nominee-2028
# Capture a draft entry to stdout for review (does not edit the registry):
go run ./cmd/reviewed -capture -pair-id my-pair -polymarket-id 559652 -kalshi-ticker KXPRESNOMD-28-GN -relation same
```

A captured entry is always `needs_review`. Approval is a manual research
judgment, recorded with reviewer, timestamp, and explicit evidence for outcome,
timing, exceptional settlement, and sources; unresolved findings prohibit
approval. The file is not an authenticated approval system. Nonempty evidence
fields do not prove that a reviewer is correct. Do not approve merely to enable
quote calculations. The supplied registry deliberately contains no approved
pairs while material uncertainties remain.

The fingerprint covers original titles/rules, displayed deadlines and expiry
conditions, source identities, contract/asset IDs, outcome mapping, and the
bytes of the linked Kalshi binding-terms PDF. Binding terms are fetched only
from HTTPS `assets.kalshi.com/contract_terms/` URLs, with bounded size, timeout,
and redirects. Fees, liquidity and prices are excluded from the fingerprint;
fee parameters are refreshed before approved quotes are priced. This watches
the captured market/terms inputs, not every referenced rulebook or source webpage.
Review platform-wide rule changes separately. Byte-level PDF
or cosmetic rule changes conservatively invalidate the saved review too.

Rejected entries never fetch quotes. Pending entries refresh rules but withhold
quotes. Approved entries refresh rules and terms first: a changed fingerprint
returns `rules_changed`, and failed rule retrieval returns `unverifiable`;
both withhold pricing. The tool never updates a saved fingerprint or review
status automatically. Recapture and review material changes before replacing
an entry. The JSON reader rejects unknown fields, duplicate IDs, bad hashes,
invalid versions, and incomplete approval evidence.

Only `approved_rules_current` entries fetch concurrent books and fee metadata.
Existing exact cost/fee, outcome-mapping, minimum-size, top-of-book quantity,
and freshness checks apply. Quote changes do not invalidate approval; stale
quotes withhold the comparison. Kalshi's REST response still lacks an exchange
timestamp. Any payout-minus-cost result remains conditional on fills, collateral
parity, fee behavior, and the validity of the manual settlement review. Approval
is not a guaranteed-profit claim. This command never places orders or accesses
credentials. Existing matcher, scanner and paircheck commands are unchanged.


## Settlement-scenario validator

```sh
# Newsom payout table, first checking live rules and binding PDF fingerprint:
go run ./cmd/settlement
# Same table using saved rules only (no current-rule claim):
go run ./cmd/settlement -offline
# Structured report:
go run ./cmd/settlement -format json
```

The initial supported scenario profile is `presidential_nomination`; other
contract families need their own defined coverage profile rather than reusing
nomination cases. The Newsom registry entry includes eight documented scenarios: first
acceptance by Newsom or someone else, both replacement directions, no accepted
nominee, late acceptance between the listed terminal times, cancellation,
and disputed acceptance. Four ordinary/replacement scenarios align in the
manual model. Four exceptional scenarios remain unresolved, so the total
payout floor remains unknown and approval stays blocked. The validator never
turns an unclear or void outcome into a $0/$1 assumption.

For the recorded same-direction mapping it calculates Polymarket YES + Kalshi
NO and Polymarket NO + Kalshi YES. An inverted mapping uses YES + YES and
NO + NO. A known incompatible case exposes the $0/$2 payout rather than
reporting an equivalent hedge. Amounts are nominal per one unit on each venue,
before purchase costs, fees or currency effects. A positive trading margin
is not calculated by this command.

Scenario models reference exact rule fingerprints and evidence IDs. Snapshot
excerpts must occur in the cited venue's saved rule field. Binding-PDF evidence
must reference the correct document URL/hash; its quotation and interpretation
remain manually supplied, not machine-verified PDF semantics. Models declare
required scenario categories, unique case/source IDs, individual payout
assertions and reasons. Invalid evidence, wrong mappings, stale rule bindings,
missing categories, unknown or void payouts cannot clear the review.

A fully populated, arithmetically compatible table is `compatible_in_model`,
not automatically approved or proof that the scenarios exhaust all possible
settlements. Registry approval additionally requires the existing manual
review evidence and the exact scenario-model fingerprint. Editing the table
invalidates that attestation. Legacy approved entries without a scenario
model now fail validation; they must be reviewed and upgraded, not grandfathered
into quote fetching. Pending/rejected entries without models remain readable.

Default execution fetches current public metadata and the binding PDF before
showing the table. Changed rules return `rules_changed` and withhold the stale
table. Retrieval errors fail the check. `-offline` explicitly uses archived
rules only. No registry file, review status, credentials or orders are modified.


### Focused official-source review

`reports/newsom-rules-review.json` records the source-backed findings and
unanswered questions. The resolution guide distinguishes Polymarket end-date
eligibility from a hard event cutoff, and its dispute documentation permits
rare nonbinary outcomes. The table wording was corrected accordingly; four
exceptional cases remain unresolved and the pair stays unapproved.

Market/event/question clarification requests timed out, so the report does
not claim that no clarifications exist. The current Kalshi general rulebook
version could not be verified; older search results were not applied as
current authority. The contract's specific PDF was rechecked against its
recorded hash. Pending questions are recorded for platform clarification;
no messages have been sent. Rule fingerprints do not cover every external
rulebook or bulletin-board update and are not an exhaustive review guarantee.

`reports/november-unemployment-pair-review.json` documents an alternative
BLS U-3 pair. Its ordinary monthly predicates align inversely, but delayed
release and revision windows differ. A hypothetical delay exposes a zero-payout
package. The example numbers are illustrative, not observed unemployment data.
This candidate was not approved. No matcher thresholds or approval gates were
relaxed as a result of the research.
