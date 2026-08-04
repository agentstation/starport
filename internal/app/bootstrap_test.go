package app

func withBootstrapFactories(factories bootstrapFactories) Option {
	return func(options *buildOptions) { options.factories = factories }
}
