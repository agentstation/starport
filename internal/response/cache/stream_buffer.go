package cache

import (
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

const (
	// StreamCacheByteLimit bounds charged retention for one active stream.
	StreamCacheByteLimit = 256 << 10
	// StreamCacheEventLimit bounds event bookkeeping independently of payload size.
	StreamCacheEventLimit = 1024
)

// StreamBuffer retains optional cache input. A limit breach discards all input.
// The caller continues delivering the original events independently.
type StreamBuffer struct {
	events   []inference.StreamEvent
	cost     int
	disabled bool
}

// Add copies an event only while both retention limits permit it.
func (b *StreamBuffer) Add(event inference.StreamEvent) {
	if b.disabled {
		return
	}
	if len(b.events) >= StreamCacheEventLimit {
		b.Discard()
		return
	}
	cost, ok := streamEventCost(event, StreamCacheByteLimit-b.cost)
	if !ok {
		b.Discard()
		return
	}
	clone := event.Clone()
	ownStreamStrings(&clone)
	b.events = append(b.events, clone)
	b.cost += cost
}

// Events returns retained input for completion before Discard.
func (b *StreamBuffer) Events() []inference.StreamEvent { return b.events }

// Discard releases retained input and prevents subsequent accumulation.
func (b *StreamBuffer) Discard() {
	b.events = nil
	b.cost = 0
	b.disabled = true
}

// RetainedBytes reports the conservative byte charge, not measured heap size.
func (b *StreamBuffer) RetainedBytes() int { return b.cost }

type streamCharge struct {
	remaining int
	valid     bool
}

func (c *streamCharge) add(n int) {
	if n < 0 || n > c.remaining {
		c.valid = false
		return
	}
	c.remaining -= n
}

func (c *streamCharge) payload(n int) {
	// Allow allocator rounding without overflowing on oversized provider data.
	if n > c.remaining/2 {
		c.valid = false
		return
	}
	c.add(n*2 + 32)
}

func (c *streamCharge) text(values ...string) {
	for _, value := range values {
		c.payload(len(value))
	}
}

func streamEventCost(e inference.StreamEvent, limit int) (int, bool) {
	c := streamCharge{remaining: limit, valid: true}
	// Fixed charges include slice growth and structures on supported 64-bit hosts.
	c.add(512)
	c.text(string(e.Kind), e.ID, e.Model, e.ModelUsed, e.SystemFingerprint)
	if e.Usage != nil {
		c.add(128)
	}
	for _, d := range e.Deltas {
		if !c.valid {
			return 0, false
		}
		c.add(512)
		c.text(string(d.Role), d.Text, d.Reasoning, d.FinishReason)
		if d.Audio != nil {
			c.add(64)
			c.payload(len(d.Audio.Data))
			c.text(d.Audio.Transcript)
		}
		for _, call := range d.ToolCalls {
			if !c.valid {
				return 0, false
			}
			c.add(128)
			c.text(call.ID, call.Name, call.Arguments)
		}
		for _, lp := range d.LogProbs {
			if !c.valid {
				return 0, false
			}
			c.add(192)
			c.text(lp.Token)
			if len(lp.Bytes) > c.remaining/16 {
				return 0, false
			}
			c.add(len(lp.Bytes) * 16)
			for _, top := range lp.Top {
				if !c.valid {
					return 0, false
				}
				c.add(128)
				c.text(top.Token)
				if len(top.Bytes) > c.remaining/16 {
					return 0, false
				}
				c.add(len(top.Bytes) * 16)
			}
		}
		for _, p := range d.Media {
			if !c.valid {
				return 0, false
			}
			c.add(256)
			c.text(string(p.Kind), p.Text, p.CacheControl)
			if p.Image != nil {
				c.add(64)
				c.text(p.Image.URL, p.Image.Detail)
			}
			if p.Audio != nil {
				c.add(128)
				c.text(p.Audio.URL, p.Audio.Format)
				c.payload(len(p.Audio.Data))
			}
			if p.Document != nil {
				c.add(192)
				c.text(p.Document.URL, p.Document.Format, p.Document.Filename, p.Document.FileID)
				c.payload(len(p.Document.Data))
			}
			if p.Video != nil {
				c.add(128)
				c.text(p.Video.URL, p.Video.Format)
				c.payload(len(p.Video.Data))
			}
		}
	}
	return limit - c.remaining, c.valid
}

// Clone strings so small substrings cannot retain large provider buffers.
func ownStreamStrings(e *inference.StreamEvent) {
	e.Kind = inference.StreamEventKind(strings.Clone(string(e.Kind)))
	e.ID = strings.Clone(e.ID)
	e.Model = strings.Clone(e.Model)
	e.ModelUsed = strings.Clone(e.ModelUsed)
	e.SystemFingerprint = strings.Clone(e.SystemFingerprint)
	for i := range e.Deltas {
		d := &e.Deltas[i]
		d.Role = inference.Role(strings.Clone(string(d.Role)))
		d.Text = strings.Clone(d.Text)
		d.Reasoning = strings.Clone(d.Reasoning)
		d.FinishReason = strings.Clone(d.FinishReason)
		if d.Audio != nil {
			d.Audio.Transcript = strings.Clone(d.Audio.Transcript)
		}
		for j := range d.ToolCalls {
			call := &d.ToolCalls[j]
			call.ID = strings.Clone(call.ID)
			call.Name = strings.Clone(call.Name)
			call.Arguments = strings.Clone(call.Arguments)
		}
		for j := range d.LogProbs {
			lp := &d.LogProbs[j]
			lp.Token = strings.Clone(lp.Token)
			for k := range lp.Top {
				lp.Top[k].Token = strings.Clone(lp.Top[k].Token)
			}
		}
		for j := range d.Media {
			p := &d.Media[j]
			p.Kind = inference.ContentKind(strings.Clone(string(p.Kind)))
			p.Text = strings.Clone(p.Text)
			p.CacheControl = strings.Clone(p.CacheControl)
			if p.Image != nil {
				p.Image.URL = strings.Clone(p.Image.URL)
				p.Image.Detail = strings.Clone(p.Image.Detail)
			}
			if p.Audio != nil {
				p.Audio.URL = strings.Clone(p.Audio.URL)
				p.Audio.Format = strings.Clone(p.Audio.Format)
			}
			if p.Document != nil {
				p.Document.URL = strings.Clone(p.Document.URL)
				p.Document.Format = strings.Clone(p.Document.Format)
				p.Document.Filename = strings.Clone(p.Document.Filename)
				p.Document.FileID = strings.Clone(p.Document.FileID)
			}
			if p.Video != nil {
				p.Video.URL = strings.Clone(p.Video.URL)
				p.Video.Format = strings.Clone(p.Video.Format)
			}
		}
	}
}
