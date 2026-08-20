package router

import "strings"

// variantEffects are the routing effects of the OpenRouter model variant
// suffixes present in one request.
type variantEffects struct {
	sortPrice       bool
	sortLatency     bool
	zeroPriceModels []string
}

// parseModelVariants strips the documented OpenRouter variant suffixes from
// the requested model IDs and reports their routing effects: ":floor" sorts
// by price, ":nitro" sorts by measured latency, and ":free" restricts the
// stripped model to zero-price offerings. Any other suffix stays untouched
// because provider model IDs are opaque.
func parseModelVariants(models []string) ([]string, variantEffects) {
	effects := variantEffects{}
	stripped := make([]string, len(models))
	for index, model := range models {
		base, variant := cutVariant(model)
		stripped[index] = base
		switch variant {
		case "floor":
			effects.sortPrice = true
		case "nitro":
			effects.sortLatency = true
		case "free":
			effects.zeroPriceModels = append(effects.zeroPriceModels, base)
		}
	}
	return stripped, effects
}

func cutVariant(model string) (string, string) {
	index := strings.LastIndex(model, ":")
	if index <= 0 {
		return model, ""
	}
	switch variant := model[index+1:]; variant {
	case "floor", "nitro", "free":
		return model[:index], variant
	default:
		return model, ""
	}
}
