package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxCredentialPayloadBytes = 1 << 20
	secretResourceCollection  = "secrets"
)

func defaultDirectSecretSources() []ReferenceSource {
	return []ReferenceSource{
		newGCPSecretManagerSource(),
		newAzureKeyVaultSource(),
		newAWSSecretsManagerSource(),
		newVaultSource(),
		newOpenBaoSource(),
	}
}

func scalarSecretMaterial(
	backend ReferenceBackend,
	payload []byte,
	field string,
	version string,
) (SourceMaterial, error) {
	if len(payload) == 0 {
		return SourceMaterial{}, NewSourceError(SourceErrorNotConfigured, backend)
	}
	if len(payload) > maxCredentialPayloadBytes || version == "" {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, backend)
	}
	if field == "" {
		return NewSourceMaterial(
			map[string]string{sourceScalarField: string(payload)}, version, time.Time{}, nil,
		), nil
	}
	value, found, err := selectJSONStringField(payload, field)
	if err != nil {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, backend)
	}
	if !found || value == "" {
		return SourceMaterial{}, NewSourceError(SourceErrorNotConfigured, backend)
	}
	return NewSourceMaterial(map[string]string{field: value}, version, time.Time{}, nil), nil
}

func selectJSONStringField(payload []byte, field string) (string, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false, errInvalidSecretObject
	}
	seen := make(map[string]struct{})
	selected := json.RawMessage(nil)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, ok := keyToken.(string)
		if keyErr != nil || !ok {
			return "", false, errInvalidSecretObject
		}
		if _, exists := seen[key]; exists {
			return "", false, errInvalidSecretObject
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if decodeErr := decoder.Decode(&raw); decodeErr != nil {
			return "", false, errInvalidSecretObject
		}
		if key == field {
			selected = append(selected[:0], raw...)
		}
	}
	if _, err = decoder.Token(); err != nil {
		return "", false, errInvalidSecretObject
	}
	if err = rejectTrailingJSON(decoder); err != nil {
		return "", false, err
	}
	if selected == nil {
		return "", false, nil
	}
	var value string
	if err = json.Unmarshal(selected, &value); err != nil {
		return "", false, errInvalidSecretObject
	}
	return value, true, nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errInvalidSecretObject
	}
	return nil
}

var errInvalidSecretObject = errors.New("invalid secret object")

func sourceFailure(
	ctx context.Context,
	backend ReferenceBackend,
	err error,
	classify func(error) SourceErrorKind,
) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return NewSourceError(classify(err), backend)
}

func ownedHTTPClient() (*http.Client, func() error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}, func() error { return nil }
	}
	ownedTransport := transport.Clone()
	client := &http.Client{
		Transport: ownedTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client, func() error {
		ownedTransport.CloseIdleConnections()
		return nil
	}
}

func hasControlCharacter(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return character < ' ' || character == 0x7f
	}) >= 0
}
