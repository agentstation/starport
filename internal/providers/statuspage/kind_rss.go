package statuspage

import (
	"context"
	"encoding/xml"
	"strings"
	"time"
)

// maxRSSBytes caps an incident feed. Feeds carry incident history, so they
// run larger than a status document.
const maxRSSBytes = 512 * 1024

// rssActiveWindow bounds how old a feed item can be and still assert a live
// incident. A feed has no structured resolved flag, so without this bound a
// final update that never says "resolved" would assert an incident forever.
const rssActiveWindow = 24 * time.Hour

// rssFeed reads both feed dialects: RSS items and Atom entries.
type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Link        string `xml:"link"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Content string `xml:"content"`
	Updated string `xml:"updated"`
	Link    struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// readRSS answers from an incident feed. A feed carries one item per
// incident update and no structured component status, so the newest item
// decides: an unresolved update inside the active window reads as a minor
// incident carrying the item's title, and anything else reads as none. The
// severity is deliberately conservative — a feed states that something
// happened, not how much of the service it took down.
func (p *Poller) readRSS(ctx context.Context, target Target) (verdict, bool) {
	body, ok := p.fetch(ctx, target.URL, maxRSSBytes)
	if !ok {
		return verdict{}, false
	}
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return verdict{}, false
	}
	title, text, published, found := latestFeedItem(feed)
	if !found {
		// An empty feed is still an answered page with nothing open.
		return verdict{indicator: IndicatorNone}, true
	}
	if rssResolved(title, text) || !withinActiveWindow(published, p.clock(), rssActiveWindow) {
		return verdict{indicator: IndicatorNone}, true
	}
	return verdict{indicator: IndicatorMinor, description: strings.TrimSpace(title)}, true
}

func latestFeedItem(feed rssFeed) (title, text, published string, found bool) {
	if len(feed.Channel.Items) > 0 {
		item := feed.Channel.Items[0]
		return item.Title, item.Description, item.PubDate, true
	}
	if len(feed.Entries) > 0 {
		entry := feed.Entries[0]
		return entry.Title, entry.Content, entry.Updated, true
	}
	return "", "", "", false
}

// historyRSS reads the whole feed as the incident log it is: one item per
// incident, newest first. A feed states neither severity nor closure time,
// so an item carries an indicator only while it reads as live under the
// same rule the poller uses, and a resolved item keeps a zero ResolvedAt.
func (r *HistoryReader) historyRSS(ctx context.Context, target Target) ([]Incident, bool) {
	body, ok := fetchDocument(ctx, r.client, target.URL, maxRSSBytes)
	if !ok {
		return nil, false
	}
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, false
	}
	now := r.clock()
	incidents := make([]Incident, 0, len(feed.Channel.Items)+len(feed.Entries))
	for _, item := range feed.Channel.Items {
		incidents = append(incidents, feedIncident(item.Title, item.Description, item.PubDate, item.Link, now))
	}
	for _, entry := range feed.Entries {
		incidents = append(incidents, feedIncident(entry.Title, entry.Content, entry.Updated, entry.Link.Href, now))
	}
	return incidents, true
}

// feedIncident normalizes one feed item. Status is only asserted where the
// feed asserts it: resolved when the item says so, active while the item
// is recent enough to assert a live incident, and unstated in between.
func feedIncident(title, text, published, link string, now time.Time) Incident {
	incident := Incident{
		Title:     strings.TrimSpace(title),
		StartedAt: parseFeedTime(published),
		URL:       strings.TrimSpace(link),
		Update:    truncateRunes(stripMarkup(text), maxHistoryUpdateRunes),
	}
	switch {
	case rssResolved(title, text):
		incident.Status = "resolved"
	case withinActiveWindow(published, now, rssActiveWindow):
		incident.Status = "active"
		incident.Indicator = IndicatorMinor
	}
	return incident
}

// parseFeedTime reads the timestamp formats the two feed dialects use.
func parseFeedTime(published string) time.Time {
	published = strings.TrimSpace(published)
	if published == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339} {
		if stamp, err := time.Parse(layout, published); err == nil {
			return stamp.UTC()
		}
	}
	return time.Time{}
}

// rssResolved reports whether an item announces its own resolution. Status
// feeds close an incident with a final update whose title or body carries
// the word.
func rssResolved(title, text string) bool {
	combined := strings.ToLower(title + " " + text)
	return strings.Contains(combined, "resolved") || strings.Contains(combined, "completed")
}

// withinActiveWindow reports whether the item's timestamp is recent enough
// to assert a live incident. An unparseable or absent timestamp is not: the
// feed then offers no way to age the incident out.
func withinActiveWindow(published string, now time.Time, window time.Duration) bool {
	stamp := parseFeedTime(published)
	if stamp.IsZero() {
		return false
	}
	return now.Sub(stamp) <= window
}
