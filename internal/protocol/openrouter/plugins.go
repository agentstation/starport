package openrouter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// OpenRouter carries gateway work in a `plugins` array. Each entry names an
// identifier and configures it. This gateway enforces exactly one identifier,
// `file-parser`, which turns an attached document into text before a model
// reads it.
//
// Every other identifier draws a refusal. Accepting one and doing nothing is
// the failure this file exists to end: a caller that named a plugin got a
// normal answer, paid for it, and had no way to learn that the plugin never
// ran. A refusal that names the enforced set costs the caller one request and
// tells it exactly what this deployment does.

// pluginIDFileParser is the one plugin identifier this gateway enforces.
const pluginIDFileParser = "file-parser"

var (
	// ErrUnenforcedPlugin reports a plugin identifier this gateway does not
	// run. It is a refusal rather than a report, because a plugin changes
	// what the model reads: serving the request without it answers a
	// different question than the caller asked.
	ErrUnenforcedPlugin = errors.New("plugin is not enforced by this gateway")
	// ErrUnknownParserEngine reports a parser engine outside the vocabulary
	// this gateway runs.
	ErrUnknownParserEngine = errors.New("file-parser engine is not served by this gateway")
	// ErrDuplicateFileParser reports a request that configures the parser
	// twice. Two entries can name two engines, and picking either one
	// silently would extract the document a way the caller did not choose.
	ErrDuplicateFileParser = errors.New("plugins names file-parser more than once")
)

// pluginEnvelope reads the identifier out of one plugin entry. It is decoded
// first and on its own, so an entry for an unenforced plugin draws a refusal
// naming that plugin rather than a decode failure about fields belonging to
// the parser.
type pluginEnvelope struct {
	ID string `json:"id"`
}

// fileParserPlugin is the `file-parser` entry. OpenRouter nests the engine
// under the document kind, which leaves room for a second kind later without
// changing the entries a caller already sends.
type fileParserPlugin struct {
	ID  string         `json:"id"`
	PDF *fileParserPDF `json:"pdf,omitempty"`
}

type fileParserPDF struct {
	Engine string `json:"engine,omitempty"`
}

// decodePlugins turns the plugins array into the canonical parser option.
//
// An empty array and an absent field both mean the caller asked for no
// extraction, which is not the same as asking for the native engine. A
// `file-parser` entry that names no engine gets native, because the in-process
// read is the one that costs nothing and needs no catalogued offering.
func decodePlugins(entries []json.RawMessage) (inference.DocumentParser, error) {
	var parser inference.DocumentParser
	for index, entry := range entries {
		var envelope pluginEnvelope
		if err := decodeStrictBytes(entry, &envelope, false); err != nil {
			return inference.DocumentParser{}, fmt.Errorf("plugins[%d]: %w", index, err)
		}
		if envelope.ID != pluginIDFileParser {
			return inference.DocumentParser{}, fmt.Errorf(
				"plugins[%d]: %q: %w (this gateway enforces %q)",
				index, envelope.ID, ErrUnenforcedPlugin, pluginIDFileParser)
		}
		if parser.Requested() {
			return inference.DocumentParser{}, fmt.Errorf("plugins[%d]: %w", index, ErrDuplicateFileParser)
		}
		var wire fileParserPlugin
		if err := decodeStrictBytes(entry, &wire, true); err != nil {
			return inference.DocumentParser{}, fmt.Errorf("plugins[%d]: %w", index, err)
		}
		engine, err := decodeParserEngine(wire)
		if err != nil {
			return inference.DocumentParser{}, fmt.Errorf("plugins[%d]: %w", index, err)
		}
		parser.Engine = engine
	}
	return parser, nil
}

// decodeParserEngine reads the engine name, defaulting to native.
func decodeParserEngine(wire fileParserPlugin) (inference.ParserEngine, error) {
	if wire.PDF == nil || wire.PDF.Engine == "" {
		return inference.ParserEngineNative, nil
	}
	engine := inference.ParserEngine(wire.PDF.Engine)
	if !engine.Known() {
		return "", fmt.Errorf("pdf.engine %q: %w (served: %s)",
			wire.PDF.Engine, ErrUnknownParserEngine, servedParserEngines())
	}
	return engine, nil
}

// servedParserEngines renders the vocabulary for a refusal message.
func servedParserEngines() string {
	names := make([]string, 0, len(inference.KnownParserEngines()))
	for _, engine := range inference.KnownParserEngines() {
		names = append(names, string(engine))
	}
	return strings.Join(names, ", ")
}

// decodeStrictBytes decodes one recorded JSON value. The envelope pass reads
// the identifier alone and therefore tolerates the fields that belong to
// whichever plugin the entry names; the second pass rejects an unknown field,
// so a misspelled parser option draws a refusal rather than a default.
func decodeStrictBytes(raw json.RawMessage, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}
