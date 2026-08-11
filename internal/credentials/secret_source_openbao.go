package credentials

import (
	"context"
	"errors"
	"net/http"

	openbao "github.com/openbao/openbao/api/v2"
)

type openBaoSecretRead = kvV2Read[openbao.KVSecret]

type openBaoSecretOpen func() (openBaoSecretRead, func() error, error)

type openBaoSource struct {
	open openBaoSecretOpen
}

func newOpenBaoSource() *openBaoSource {
	return &openBaoSource{open: func() (openBaoSecretRead, func() error, error) {
		return openKVV2Read(func() (kvV2MountReader[openbao.KVSecret], *http.Client, error) {
			config := openbao.DefaultConfig()
			if config == nil || config.Error != nil {
				return nil, nil, errInvalidSecretObject
			}
			client, err := openbao.NewClient(config)
			if err != nil {
				return nil, nil, err
			}
			return func(mount string) kvV2SecretReader[openbao.KVSecret] {
				return client.KVv2(mount)
			}, config.HttpClient, nil
		})
	}}
}

func (*openBaoSource) Backend() ReferenceBackend { return ReferenceBackendOpenBao }

func (s *openBaoSource) Resolve(
	ctx context.Context,
	reference Reference,
) (material SourceMaterial, err error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return SourceMaterial{}, contextErr
	}
	mount, path, version, parseErr := kvV2SecretResource(reference)
	if parseErr != nil {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, s.Backend())
	}
	read, closeClient, openErr := s.open()
	if openErr != nil {
		return SourceMaterial{}, sourceFailure(ctx, s.Backend(), openErr, func(error) SourceErrorKind {
			return SourceErrorUnavailable
		})
	}
	defer func() {
		if closeErr := closeClient(); closeErr != nil && err == nil {
			material = SourceMaterial{}
			err = NewSourceError(SourceErrorUnavailable, s.Backend())
		}
	}()
	secret, readErr := read(ctx, mount, path, version)
	if readErr != nil {
		return SourceMaterial{}, sourceFailure(ctx, s.Backend(), readErr, classifyOpenBaoSecretError)
	}
	if secret == nil || secret.VersionMetadata == nil {
		return SourceMaterial{}, NewSourceError(SourceErrorNotConfigured, s.Backend())
	}
	return kvV2SourceMaterial(
		s.Backend(), secret.Data, secret.VersionMetadata.Version, reference.field,
	)
}

func classifyOpenBaoSecretError(err error) SourceErrorKind {
	if errors.Is(err, openbao.ErrSecretNotFound) {
		return SourceErrorNotConfigured
	}
	var responseErr *openbao.ResponseError
	if !errors.As(err, &responseErr) {
		return SourceErrorUnavailable
	}
	switch responseErr.StatusCode {
	case http.StatusNotFound:
		return SourceErrorNotConfigured
	case http.StatusUnauthorized, http.StatusForbidden:
		return SourceErrorDenied
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return SourceErrorInvalid
	default:
		return SourceErrorUnavailable
	}
}
