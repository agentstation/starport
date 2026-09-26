package cache

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
)

const modelScalarLimit = 512 << 10
const modelContainerLimit = 65536

var errModelEncodingLimit = errors.New("model cache encoding exceeds its optional budget")

var modelMarshalers = json.MarshalToFunc(func(_ *jsontext.Encoder, value any) error {
	v := reflect.ValueOf(value)
	for depth := 0; v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface; depth++ {
		if depth == 64 {
			return errModelEncodingLimit
		}
		if v.IsNil() {
			return errors.ErrUnsupported
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		if v.Len() > modelScalarLimit {
			return errModelEncodingLimit
		}
	case reflect.Slice:
		limit := modelContainerLimit
		if v.Type().Elem().Kind() == reflect.Uint8 {
			limit = modelScalarLimit
		}
		if v.Len() > limit {
			return errModelEncodingLimit
		}
	case reflect.Array, reflect.Map:
		if v.Len() > modelContainerLimit {
			return errModelEncodingLimit
		}
	}
	return errors.ErrUnsupported
})

// encodeModel caps stored bytes and large scalars before their encoded copies.
// Legacy JSON options preserve the existing cache representation.
func encodeModel(ctx context.Context, model any, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, errModelEncodingLimit
	}
	out := &modelEncodingWriter{ctx: ctx, limit: limit}
	err := json.MarshalWrite(out, model, jsonv1.DefaultOptionsV1(), json.WithMarshalers(modelMarshalers))
	if err != nil {
		return nil, err
	}
	return out.data, nil
}

type modelEncodingWriter struct {
	ctx   context.Context
	data  []byte
	limit int
}

func (w *modelEncodingWriter) Write(value []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(value) > w.limit-len(w.data) {
		return 0, errModelEncodingLimit
	}
	w.data = append(w.data, value...)
	return len(value), nil
}
