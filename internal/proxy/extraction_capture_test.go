package proxy

import (
	"context"
	"errors"
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"io"
	"testing"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/stretchr/testify/require"
)

type recognitionAccountingRouter struct {
	*recognizingRouter
	stream execution.ManagedStream
	err    error
}

func (r *recognitionAccountingRouter) RouteWithFallback(ctx context.Context, req *routepkg.Request) (*routepkg.Response, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.capturingRouter.RouteWithFallback(ctx, req)
}
func (r *recognitionAccountingRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return r.stream, r.err
}

func TestExtractionCaptureSurvivesFailureAndStreaming(t *testing.T) {
	for _, test := range []struct {
		name                        string
		short, stream, fail, cancel bool
	}{
		{name: "short recognition", short: true},
		{name: "chat failure", fail: true},
		{name: "stream complete", stream: true},
		{name: "stream setup failure", stream: true, fail: true},
		{name: "stream cancellation", stream: true, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recognizer := newRecognizingRouter("Recognized text")
			recognizer.usage = &inference.Usage{InputTokens: 100, OutputTokens: 30, TotalTokens: 130, CacheReadTokens: 20}
			if test.short {
				recognizer.pages = nil
			}
			router := &recognitionAccountingRouter{recognizingRouter: recognizer, stream: &evidenceStream{model: "openai/gpt-4o"}}
			if test.fail {
				router.err = errors.New("provider failed")
			}
			prices := recognitionPrices()
			prices.offerings = map[string]starmapcatalogs.ProviderOffering{"google/gemini-2.5-flash": tokenRecognitionOffering()}
			repository := &recordingUsageRepository{}
			capture := NewUsageCapture(repository)
			service := capture.Wrap(&proxy{router: router, prices: prices})
			req := parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition)
			ctx := catalogContext(t)
			if test.stream {
				req.Request.Stream = true
				stream, err := service.ProcessChatCompletionStream(ctx, req)
				if test.fail {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					if !test.cancel {
						_, err = stream.Read()
						require.ErrorIs(t, err, io.EOF)
					}
					require.NoError(t, stream.Close())
				}
			} else {
				_, err := service.ProcessChatCompletion(ctx, req)
				require.Error(t, err)
			}
			capture.Flush()
			records := repository.all()
			require.Len(t, records, 1)
			require.Len(t, records[0].Extractions, 1)
			require.EqualValues(t, 150000, records[0].Extractions[0].Cost.NanoUSD)
			require.EqualValues(t, 150000, records[0].ExtractionCost.NanoUSD)
			require.EqualValues(t, 130, records[0].Extractions[0].Tokens.Total)
		})
	}
}
