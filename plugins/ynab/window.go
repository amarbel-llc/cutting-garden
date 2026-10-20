package ynab

import (
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// dateWindow is the since_date / until_date pair one transaction request
// sends (YYYY-MM-DD, until may be empty). YNAB has no paging, only date
// bounds, so a "page" is a window (FDR 0027 §Paging): the default is the last
// defaultWindowDays, and a `date` filter names another.
type dateWindow struct {
	since, until string
	// explicit is true when the window came from a date predicate, so the
	// caller asked for exactly that span; false for the short default window,
	// whose summaries must say they do not cover the whole history.
	explicit bool
}

// windowFromFilter derives the request window from filter's date predicates:
// `date=2026` is the year, `date=2026-07` the month, `date=2026-07-15` the
// day. Several date predicates intersect, as the filter's predicates are
// AND-composed. With none, it is the default window.
func windowFromFilter(filter cutting_garden_plugins.FacetFilter) (dateWindow, error) {
	w := dateWindow{}
	for _, pred := range filter {
		if pred.Dimension != facetDate {
			continue
		}
		start, end, err := dateBucketBounds(pred.Value)
		if err != nil {
			return dateWindow{}, err
		}
		w.explicit = true
		if w.since == "" || start > w.since {
			w.since = start
		}
		if w.until == "" || end < w.until {
			w.until = end
		}
	}
	if !w.explicit {
		w.since = defaultSince()
	}
	return w, nil
}

// withoutDate drops the date predicates from filter. The window already
// enforced them exactly (the server did the narrowing), and an unvalidated
// predicate would otherwise compare a day key for equality rather than by
// prefix.
func withoutDate(filter cutting_garden_plugins.FacetFilter) cutting_garden_plugins.FacetFilter {
	var rest cutting_garden_plugins.FacetFilter
	for _, pred := range filter {
		if pred.Dimension != facetDate {
			rest = append(rest, pred)
		}
	}
	return rest
}

// dateBucketBounds is the first and last day (YYYY-MM-DD) of a date bucket:
// a year ("2026"), a month ("2026-07") or a day ("2026-07-15").
func dateBucketBounds(value string) (first, last string, err error) {
	const day = "2006-01-02"
	switch len(value) {
	case 4:
		if y, perr := time.Parse("2006", value); perr == nil {
			return y.Format(day), y.AddDate(1, 0, -1).Format(day), nil
		}
	case 7:
		if m, perr := time.Parse("2006-01", value); perr == nil {
			return m.Format(day), m.AddDate(0, 1, -1).Format(day), nil
		}
	case 10:
		if _, perr := time.Parse(day, value); perr == nil {
			return value, value, nil
		}
	}
	return "", "", errors.BadRequestf(
		"ynab plugin: date filter %q is not a year (2026), month (2026-07) or day (2026-07-15)", value,
	)
}
