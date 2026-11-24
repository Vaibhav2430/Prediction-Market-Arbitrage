package matching

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Facts are conservative extractions, not a semantic proof. Empty fields are
// unknown. Evidence identifies original text used by each recognized pattern.
type Facts struct {
	Subject     string            `json:"subject,omitempty"`
	Kind        string            `json:"contract_type,omitempty"`
	Direction   string            `json:"direction,omitempty"`
	Lower       string            `json:"lower_amount,omitempty"`
	Upper       string            `json:"upper_amount,omitempty"`
	Mode        string            `json:"observation_mode,omitempty"`
	Source      string            `json:"resolution_source_family,omitempty"`
	Measurement string            `json:"measurement,omitempty"`
	Observation Observation       `json:"observation"`
	Evidence    map[string]string `json:"evidence"`
	Unknown     []string          `json:"unknown_details"`
}

type Observation struct {
	Year  int    `json:"year,omitempty"`
	Month int    `json:"month,omitempty"`
	Day   int    `json:"day,omitempty"`
	Clock string `json:"clock,omitempty"`
	Zone  string `json:"zone,omitempty"`
}

var amountPattern = `([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?\s*[kKmM]?)`
var rangePattern = regexp.MustCompile(`(?i)(?:between|from)\s*\$?` + amountPattern + `\s*(?:and|to)\s*\$?` + amountPattern)
var labelRangePattern = regexp.MustCompile(`(?i)\$` + amountPattern + `\s*(?:to|[-–])\s*\$?` + amountPattern)
var thresholdPattern = regexp.MustCompile(`(?i)\b(at least|at most|greater than or equal to|less than or equal to|greater than|less than|above|below|over|under|exceeds?|exceed|reach|hit|dip to)\s*\$?` + amountPattern)
var suffixThresholdPattern = regexp.MustCompile(`(?i)\$?` + amountPattern + `\s+or\s+(above|below|higher|lower)\b`)
var monthPattern = regexp.MustCompile(`(?i)\b(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)\.?\s+([0-9]{1,2})(?:,?\s+(20[0-9]{2}))?\b`)
var isoDayPattern = regexp.MustCompile(`\b(20[0-9]{2})-([0-9]{2})-([0-9]{2})\b`)
var clockPattern = regexp.MustCompile(`(?i)\b([0-9]{1,2})(?::([0-9]{2}))?\s*(am|pm)?\s*(?:in\s+(?:the\s+)?)?(est|edt|et|utc|gmt)\b`)
var negationPattern = regexp.MustCompile(`(?i)\b(not|never|won't|doesn't|isn't|cannot|can't)\b`)

func normalizedAmount(text string) (string, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	multiplier := int64(1)
	if strings.HasSuffix(text, "k") {
		multiplier = 1000
		text = strings.TrimSpace(strings.TrimSuffix(text, "k"))
	}
	if strings.HasSuffix(text, "m") {
		multiplier = 1000000
		text = strings.TrimSpace(strings.TrimSuffix(text, "m"))
	}
	text = strings.ReplaceAll(text, ",", "")
	if len(text) > 40 {
		return "", false
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || n.Sign() < 0 {
		return "", false
	}
	n.Mul(n, big.NewRat(multiplier, 1))
	precision := 0
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		precision = len(text) - dot - 1
	}
	canonical := n.FloatString(precision)
	if strings.Contains(canonical, ".") {
		canonical = strings.TrimRight(strings.TrimRight(canonical, "0"), ".")
	}
	return canonical, true
}

func predicate(text string) (kind, direction, lower, upper, evidence string) {
	// A negative sentence may reverse the apparent comparator. Leave it unknown
	// rather than interpreting a positive regex match inside the negation.
	if negationPattern.MatchString(text) {
		return
	}
	ranges := rangePattern.FindAllStringSubmatch(text, -1)
	if len(ranges) == 0 {
		ranges = labelRangePattern.FindAllStringSubmatch(text, -1)
	}
	if len(ranges) == 1 {
		a, okA := normalizedAmount(ranges[0][1])
		b, okB := normalizedAmount(ranges[0][2])
		if okA && okB {
			x, _ := new(big.Rat).SetString(a)
			y, _ := new(big.Rat).SetString(b)
			if x.Cmp(y) < 0 {
				return "range", "between", a, b, ranges[0][0]
			}
		}
		return
	}
	if len(ranges) > 1 {
		return
	}
	matches := thresholdPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 1 {
		value, ok := normalizedAmount(matches[0][2])
		if !ok {
			return
		}
		switch strings.ToLower(matches[0][1]) {
		case "above", "over", "greater than", "exceeds", "exceed":
			direction = "gt"
		case "below", "under", "less than":
			direction = "lt"
		case "at least", "greater than or equal to", "reach":
			direction = "gte"
		case "at most", "less than or equal to", "dip to":
			direction = "lte"
		// "hit" alone does not specify upper/lower barrier direction.
		default:
			return
		}
		return "threshold", direction, value, "", matches[0][0]
	}
	if len(matches) > 1 {
		return
	}
	suffix := suffixThresholdPattern.FindAllStringSubmatch(text, -1)
	if len(suffix) == 1 {
		value, ok := normalizedAmount(suffix[0][1])
		if !ok {
			return
		}
		direction = "gte"
		if strings.EqualFold(suffix[0][2], "below") || strings.EqualFold(suffix[0][2], "lower") {
			direction = "lte"
		}
		return "threshold", direction, value, "", suffix[0][0]
	}
	return
}

func Extract(c Contract) Facts {
	f := Facts{Evidence: map[string]string{}, Unknown: []string{}}
	title := strings.ToLower(c.Title + " " + c.YesLabel)
	rules := strings.ToLower(strings.Join(strings.Fields(c.RulesPrimary), " "))
	// Subject recognition is deliberately limited to a few unambiguous assets.
	subjects := []struct{ name, pattern string }{{"bitcoin_cash", `\b(bitcoin cash|bch)\b`}, {"bitcoin", `\b(bitcoin|btc)\b`}, {"ethereum", `\b(ethereum|ether|eth)\b`}, {"solana", `\b(solana|sol)\b`}, {"dogecoin", `\b(dogecoin|doge)\b`}}
	for _, s := range subjects {
		if regexp.MustCompile(s.pattern).MatchString(title) {
			if f.Subject == "bitcoin_cash" && s.name == "bitcoin" {
				continue
			}
			if f.Subject != "" {
				f.Subject = ""
				break
			}
			f.Subject = s.name
		}
	}
	// Primary settlement rules take precedence over display labels. If a rule
	// predicate is unsupported, preserve an unambiguous title/YES-label pattern.
	for _, input := range []struct{ label, text string }{{"primary rules", c.RulesPrimary}, {"title", c.Title}, {"YES label", c.YesLabel}} {
		kind, direction, lo, hi, evidence := predicate(input.text)
		if kind != "" {
			f.Kind, f.Direction, f.Lower, f.Upper = kind, direction, lo, hi
			f.Evidence["predicate"] = input.label + ": " + evidence
			break
		}
	}
	sourceText := strings.ToLower(c.ResolutionSource + " " + c.RulesPrimary)
	sources := []string{}
	if strings.Contains(sourceText, "binance") {
		sources = append(sources, "binance")
	}
	if strings.Contains(sourceText, "cf benchmarks") || strings.Contains(sourceText, "brti") {
		sources = append(sources, "cf_benchmarks")
	}
	if len(sources) == 1 {
		f.Source = sources[0]
		f.Evidence["resolution_source"] = c.ResolutionSource + " " + c.RulesPrimary
	}
	// A custom explicit source URL is also comparable by host; prose mentions of
	// unrelated exchanges do not establish a source. Multiple recognized sources
	// remain unknown rather than arbitrarily selecting one.
	if len(sources) == 0 && c.ResolutionSource != "" {
		if u, err := url.Parse(c.ResolutionSource); err == nil && u.Hostname() != "" {
			f.Source = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
			f.Evidence["resolution_source"] = c.ResolutionSource
		}
	}
	if strings.Contains(rules, "1 minute candle") || strings.Contains(rules, "one-minute candle") {
		if strings.Contains(rules, "\"close\"") || strings.Contains(rules, "final close") {
			f.Measurement = "one_minute_candle_close"
			f.Mode = "point"
		}
		if strings.Contains(rules, "final high") {
			f.Measurement = "one_minute_candle_high"
			f.Mode = "touch"
		}
		if strings.Contains(rules, "final low") {
			f.Measurement = "one_minute_candle_low"
			f.Mode = "touch"
		}
	}
	if (strings.Contains(rules, "sixty seconds") || strings.Contains(rules, "60 seconds") || strings.Contains(rules, "60 rti prices")) && strings.Contains(rules, "average") {
		f.Measurement = "sixty_second_index_average"
		f.Mode = "point"
	}
	if f.Mode == "" {
		if strings.Contains(title, " reach ") || strings.Contains(title, " hit ") || strings.Contains(title, " dip to ") {
			f.Mode = "touch"
		} else if strings.Contains(title, "price on ") || strings.Contains(rules, "closing at ") {
			f.Mode = "point"
		}
	}
	if f.Measurement != "" {
		f.Evidence["measurement"] = c.RulesPrimary
	}
	// Dates/clocks come from observation wording, never platform closing times.
	// A missing year remains missing; do not invent it from a trading deadline.
	for _, text := range []string{c.Title, c.RulesPrimary} {
		if o, ok := observationDate(text); ok {
			f.Observation.Year, f.Observation.Month, f.Observation.Day = o.Year, o.Month, o.Day
			f.Evidence["observation_date"] = text
			break
		}
	}
	if clock, zone, ok := observationClock(c.RulesPrimary); ok {
		f.Observation.Clock, f.Observation.Zone = clock, zone
		f.Evidence["observation_clock"] = c.RulesPrimary
	}
	for _, item := range []struct{ name, value string }{{"subject", f.Subject}, {"contract type", f.Kind}, {"direction/boundaries", f.Direction}, {"observation mode", f.Mode}, {"resolution source", f.Source}, {"measurement", f.Measurement}} {
		if item.value == "" {
			f.Unknown = append(f.Unknown, item.name)
		}
	}
	if f.Observation.Day == 0 {
		f.Unknown = append(f.Unknown, "observation date")
	}
	if f.Observation.Year == 0 {
		f.Unknown = append(f.Unknown, "observation year")
	}
	if f.Observation.Clock == "" {
		f.Unknown = append(f.Unknown, "observation time/time zone")
	}
	return f
}

func observationDate(text string) (Observation, bool) {
	dates := map[string]Observation{}
	for _, m := range monthPattern.FindAllStringSubmatch(text, -1) {
		monthNames := map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
		month := monthNames[strings.ToLower(m[1][:3])]
		day, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		if day < 1 || day > 31 {
			continue
		}
		checkYear := year
		if checkYear == 0 {
			checkYear = 2000
		}
		d := time.Date(checkYear, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if int(d.Month()) != month {
			continue
		}
		o := Observation{Year: year, Month: month, Day: day}
		dates[fmt.Sprint(year, "/", month, "/", day)] = o
	}
	for _, m := range isoDayPattern.FindAllStringSubmatch(text, -1) {
		d, err := time.Parse("2006-01-02", m[0])
		if err != nil {
			continue
		}
		o := Observation{Year: d.Year(), Month: int(d.Month()), Day: d.Day()}
		dates[fmt.Sprint(o.Year, "/", o.Month, "/", o.Day)] = o
	}
	if len(dates) != 1 {
		return Observation{}, false
	}
	for _, o := range dates {
		return o, true
	}
	return Observation{}, false
}
func observationClock(text string) (string, string, bool) {
	clocks := map[string][2]string{}
	for _, m := range clockPattern.FindAllStringSubmatch(text, -1) {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		ampm := strings.ToLower(m[3])
		if minute > 59 {
			continue
		}
		if ampm != "" {
			if hour < 1 || hour > 12 {
				continue
			}
			hour %= 12
			if ampm == "pm" {
				hour += 12
			}
		} else if m[2] == "" {
			continue
		}
		if hour > 23 {
			continue
		}
		zone := strings.ToUpper(m[4])
		if zone == "GMT" {
			zone = "UTC"
		}
		clock := fmt.Sprintf("%02d:%02d", hour, minute)
		clocks[clock+zone] = [2]string{clock, zone}
	}
	if len(clocks) != 1 {
		return "", "", false
	}
	for _, c := range clocks {
		return c[0], c[1], true
	}
	return "", "", false
}

// Conflicts returns only recognized incompatibilities. Unknowns are separately
// disclosed on surviving candidates and never interpreted as agreement.
func Conflicts(a, b Facts) []string {
	reasons := []string{}
	different := func(name, x, y string) {
		if x != "" && y != "" && x != y {
			reasons = append(reasons, name)
		}
	}
	different("subject", a.Subject, b.Subject)
	different("contract_type", a.Kind, b.Kind)
	if a.Kind != "" && a.Kind == b.Kind {
		different("threshold", a.Lower, b.Lower)
		if a.Kind == "range" {
			different("range_upper", a.Upper, b.Upper)
		}
		different("direction", a.Direction, b.Direction)
	}
	different("observation_mode", a.Mode, b.Mode)
	different("resolution_source", a.Source, b.Source)
	different("measurement", a.Measurement, b.Measurement)
	x, y := a.Observation, b.Observation
	xt, xok := observationInstant(x)
	yt, yok := observationInstant(y)
	sameInstant := xok && yok && xt.Equal(yt)
	if !sameInstant && x.Month != 0 && y.Month != 0 && (x.Month != y.Month || x.Day != y.Day || (x.Year != 0 && y.Year != 0 && x.Year != y.Year)) {
		reasons = append(reasons, "observation_date")
	}
	// Calendar dates may differ for the same instant across time zones. Never
	// reject that case merely because the printed local day differs.
	if xok && yok {
		if !sameInstant {
			reasons = append(reasons, "observation_time")
		}
	} else if x.Clock != "" && y.Clock != "" && x.Zone == y.Zone && x.Month != 0 && x.Month == y.Month && x.Day == y.Day && x.Clock != y.Clock {
		reasons = append(reasons, "observation_time")
	}
	sort.Strings(reasons)
	return reasons
}
func observationInstant(o Observation) (time.Time, bool) {
	if o.Year == 0 || o.Day == 0 || o.Clock == "" {
		return time.Time{}, false
	}
	var zone *time.Location
	switch o.Zone {
	case "UTC":
		zone = time.UTC
	case "EST":
		zone = time.FixedZone("EST", -5*3600)
	case "EDT":
		zone = time.FixedZone("EDT", -4*3600)
	case "ET":
		var err error
		zone, err = time.LoadLocation("America/New_York")
		if err != nil {
			return time.Time{}, false
		}
	default:
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-1-2 15:04", fmt.Sprintf("%d-%d-%d %s", o.Year, o.Month, o.Day, o.Clock), zone)
	return t, err == nil
}
