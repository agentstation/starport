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
}

type atomEntry struct {
	Title   string `xml:"title"`
	Content string `xml:"content"`
	Updated string `xml:"updated"`
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
	published = strings.TrimSpace(published)
	if published == "" {
		return false
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339} {
		if stamp, err := time.Parse(layout, published); err == nil {
			return now.Sub(stamp) <= window
		}
	}
	return false
}
