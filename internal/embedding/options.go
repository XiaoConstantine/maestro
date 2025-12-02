package embedding

// Option is a functional option for configuring embedding requests.
type Option func(*Config)

// Config holds routing and context configuration for embedding requests.
type Config struct {
	// Batch indicates this is a batch operation (not latency-sensitive).
	Batch bool

	// LatencyCritical indicates this embedding is needed immediately (interactive query).
	LatencyCritical bool

	// PreferLocal forces use of local models if available.
	PreferLocal bool

	// PreferCloud forces use of cloud API if available.
	PreferCloud bool

	// AllowCache enables caching of results (default: true).
	AllowCache bool

	// Model specifies the embedding model to use (e.g., "text-embedding-004", "nomic-embed-text").
	Model string

	// Context allows passing request-specific context data.
	Context map[string]interface{}
}

// WithBatch marks this as a batch operation.
func WithBatch(batch bool) Option {
	return func(c *Config) {
		c.Batch = batch
	}
}

// WithLatencyCritical marks this as latency-sensitive.
func WithLatencyCritical(critical bool) Option {
	return func(c *Config) {
		c.LatencyCritical = critical
	}
}

// WithPreferLocal prefers local embeddings.
func WithPreferLocal(prefer bool) Option {
	return func(c *Config) {
		c.PreferLocal = prefer
	}
}

// WithPreferCloud prefers cloud embeddings.
func WithPreferCloud(prefer bool) Option {
	return func(c *Config) {
		c.PreferCloud = prefer
	}
}

// WithCaching enables or disables caching.
func WithCaching(allow bool) Option {
	return func(c *Config) {
		c.AllowCache = allow
	}
}

// WithModel specifies the embedding model.
func WithModel(model string) Option {
	return func(c *Config) {
		c.Model = model
	}
}

// WithContext adds context data to the request.
func WithContext(ctx map[string]interface{}) Option {
	return func(c *Config) {
		c.Context = ctx
	}
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Batch:           false,
		LatencyCritical: false,
		PreferLocal:     false,
		PreferCloud:     false,
		AllowCache:      true,
		Model:           "",
		Context:         make(map[string]interface{}),
	}
}

// ParseOptions applies options to a config.
func ParseOptions(opts ...Option) *Config {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// ShouldUseLocal determines if local embedding should be attempted based on config.
func (c *Config) ShouldUseLocal() bool {
	// Prefer cloud for latency-critical queries
	if c.LatencyCritical && !c.PreferLocal {
		return false
	}

	// If explicitly preferring local, use local
	if c.PreferLocal {
		return true
	}

	// If explicitly preferring cloud, don't use local
	if c.PreferCloud {
		return false
	}

	// Default: use local for batch operations, cloud for interactive
	return c.Batch
}

// ShouldUseCloud determines if cloud embedding should be attempted based on config.
func (c *Config) ShouldUseCloud() bool {
	// If explicitly disabled, don't use cloud
	if c.PreferLocal {
		return false
	}

	// Cloud is always a fallback unless explicitly disabled
	return !c.PreferCloud // Only skip cloud if PreferCloud is false and PreferLocal is true
}
