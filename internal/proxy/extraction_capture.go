package proxy

import (
	"context"
	"sync"

	"github.com/agentstation/starport/internal/usage"
)

type extractionCaptureKey struct{}

type extractionCapture struct {
	mu     sync.Mutex
	report parseReport
}

// withExtractionCapture allocates accounting state only for parser requests.
func withExtractionCapture(ctx context.Context, req *ChatCompletionRequest) context.Context {
	if req == nil || !req.Request.DocumentParser.Requested() {
		return ctx
	}
	return context.WithValue(ctx, extractionCaptureKey{}, &extractionCapture{})
}

func captureExtraction(ctx context.Context, report parseReport) {
	capture, _ := ctx.Value(extractionCaptureKey{}).(*extractionCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	capture.report = report
	capture.mu.Unlock()
}

func applyCapturedExtraction(ctx context.Context, record *usage.Record, fallback *ChatCompletionResponse) {
	capture, _ := ctx.Value(extractionCaptureKey{}).(*extractionCapture)
	if capture == nil {
		applyExtraction(record, fallback)
		return
	}
	capture.mu.Lock()
	report := capture.report
	capture.mu.Unlock()
	if report.Documents == 0 {
		applyExtraction(record, fallback)
		return
	}
	response := &ChatCompletionResponse{}
	report.report(response)
	applyExtraction(record, response)
}
