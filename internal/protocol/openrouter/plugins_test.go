package openrouter

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
)

// PLG-V01 through PLG-V03. Before this file, a caller that sent a plugins
// array got a normal answer and no signal. The gateway decoded the array as
// raw JSON, listed the field as unenforced in a response header, and served
// the request as though the caller had asked for nothing.
//
// That is the failure these tests exist to prevent from returning. A plugin
// changes what the model reads. Answering without it answers a different
// question, and a header the caller has to know to look for is not a refusal.

func decodeChatBody(t *testing.T, body string) (DecodedChat, error) {
	t.Helper()
	return DecodeChat(strings.NewReader(body))
}

const chatPrefix = `{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hi"}]`

// TestARecordedFileParserPayloadRoundTripsWithoutLoss holds PLG-V01. The
// payloads are the shapes OpenRouter's own documentation publishes, so a
// client written against that documentation reaches this gateway unchanged.
func TestARecordedFileParserPayloadRoundTripsWithoutLoss(t *testing.T) {
	cases := []struct {
		name string
		body string
		want inference.ParserEngine
	}{
		{
			// The documented shape, engine named.
			name: "the documented payload names an engine",
			body: chatPrefix + `,"plugins":[{"id":"file-parser","pdf":{"engine":"native"}}]}`,
			want: inference.ParserEngineNative,
		},
		{
			name: "the recognition engine decodes",
			body: chatPrefix + `,"plugins":[{"id":"file-parser","pdf":{"engine":"recognition"}}]}`,
			want: inference.ParserEngineRecognition,
		},
		{
			// OpenRouter lets a caller name the plugin and no engine. It
			// then picks the model's own file reading first. The in-process
			// read is this gateway's equivalent, and it is the one that
			// reaches no provider and costs nothing.
			name: "a plugin with no engine defaults to native",
			body: chatPrefix + `,"plugins":[{"id":"file-parser"}]}`,
			want: inference.ParserEngineNative,
		},
		{
			name: "an empty pdf object defaults to native",
			body: chatPrefix + `,"plugins":[{"id":"file-parser","pdf":{}}]}`,
			want: inference.ParserEngineNative,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decoded, err := decodeChatBody(t, testCase.body)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if decoded.Inference.DocumentParser.Engine != testCase.want {
				t.Fatalf("engine = %q, want %q",
					decoded.Inference.DocumentParser.Engine, testCase.want)
			}
			if !decoded.Inference.DocumentParser.Requested() {
				t.Fatal("a request naming the plugin reports no parser request")
			}
		})
	}
}

// TestNoPluginsIsNotTheNativeEngine holds the other half of PLG-V01. The two
// states have to stay apart. A request that named no parser leaves an attached
// document to whatever the model does with it. A request that named the native
// engine gets extracted text whether or not the model reads documents. Folding
// the first into the second would change the answer for every request that
// attaches a file and asks for nothing.
func TestNoPluginsIsNotTheNativeEngine(t *testing.T) {
	for _, body := range []string{
		chatPrefix + `}`,
		chatPrefix + `,"plugins":[]}`,
	} {
		decoded, err := decodeChatBody(t, body)
		if err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if decoded.Inference.DocumentParser.Requested() {
			t.Fatalf("%s: reports a parser request", body)
		}
		if decoded.Inference.DocumentParser.Engine != "" {
			t.Fatalf("%s: engine = %q, want the zero value",
				body, decoded.Inference.DocumentParser.Engine)
		}
	}
}

// TestAnUnknownEngineIsRefusedRatherThanIgnored holds PLG-V02. The two vendor
// engines OpenRouter serves are the important cases here, not a nonsense
// string: this gateway routes to providers directly, and the Starmap catalog
// serves zero models under mistral and does not carry cloudflare at all.
// Accepting either name and recognizing the page some other way would report
// work this deployment did not do.
func TestAnUnknownEngineIsRefusedRatherThanIgnored(t *testing.T) {
	for _, engine := range []string{"mistral-ocr", "cloudflare-ai", "pdf-text", "Native"} {
		body := chatPrefix + `,"plugins":[{"id":"file-parser","pdf":{"engine":"` + engine + `"}}]}`
		_, err := decodeChatBody(t, body)
		if !errors.Is(err, ErrUnknownParserEngine) {
			t.Fatalf("engine %q: err = %v, want ErrUnknownParserEngine", engine, err)
		}
		// A caller that got the name wrong needs the whole vocabulary back,
		// not the news that one name failed.
		for _, served := range inference.KnownParserEngines() {
			if !strings.Contains(err.Error(), string(served)) {
				t.Fatalf("engine %q: refusal %q omits the served engine %q",
					engine, err.Error(), served)
			}
		}
	}
}

// TestAnUnenforcedPluginIsRefusedRatherThanReported holds PLG-V03. The
// identifiers below are ones OpenRouter itself publishes. This gateway runs
// none of them, and the old behavior served the request anyway.
func TestAnUnenforcedPluginIsRefusedRatherThanReported(t *testing.T) {
	for _, id := range []string{"web", "moderation", ""} {
		body := chatPrefix + `,"plugins":[{"id":"` + id + `"}]}`
		_, err := decodeChatBody(t, body)
		if !errors.Is(err, ErrUnenforcedPlugin) {
			t.Fatalf("plugin %q: err = %v, want ErrUnenforcedPlugin", id, err)
		}
		if !strings.Contains(err.Error(), pluginIDFileParser) {
			t.Fatalf("plugin %q: refusal %q does not name the enforced plugin",
				id, err.Error())
		}
	}
}

// TestTheParserIsConfiguredOnce guards the ambiguity a list creates. Two
// entries can name two engines. Reading the first would extract the document a
// way the caller did not choose, and reading the last would do the same to a
// caller who expected the first.
func TestTheParserIsConfiguredOnce(t *testing.T) {
	body := chatPrefix + `,"plugins":[` +
		`{"id":"file-parser","pdf":{"engine":"native"}},` +
		`{"id":"file-parser","pdf":{"engine":"recognition"}}]}`
	_, err := decodeChatBody(t, body)
	if !errors.Is(err, ErrDuplicateFileParser) {
		t.Fatalf("err = %v, want ErrDuplicateFileParser", err)
	}
}

// TestAMisspelledParserOptionIsRefused keeps the entry strict. A caller that
// wrote "engines" would otherwise get the native default and never learn that
// the option it typed did nothing, which is the same silence this whole file
// removes one level up.
func TestAMisspelledParserOptionIsRefused(t *testing.T) {
	body := chatPrefix + `,"plugins":[{"id":"file-parser","pdf":{"engines":"recognition"}}]}`
	_, err := decodeChatBody(t, body)
	if err == nil {
		t.Fatal("a misspelled parser option decoded without a refusal")
	}
	if errors.Is(err, ErrUnknownParserEngine) {
		t.Fatalf("err = %v, want a decode refusal naming the unknown field", err)
	}
}
