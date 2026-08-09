package app

func withRuntimeFactories(factories runtimeFactories) Option {
	return func(options *buildOptions) { options.factories = factories }
}
