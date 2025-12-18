package searchfranchises

type Config struct {
	ElasticsearchURL      string `mapstructure:"ELASTICSEARCH_URL"`
	ElasticsearchUsername string `mapstructure:"ELASTICSEARCH_USERNAME"`
	ElasticsearchPassword string `mapstructure:"ELASTICSEARCH_PASSWORD"`

	// Search configuration
	DefaultLimit int     `mapstructure:"DEFAULT_LIMIT"`
	MaxLimit     int     `mapstructure:"MAX_LIMIT"`
	Fuzziness    string  `mapstructure:"FUZZINESS"`
	MinScore     float64 `mapstructure:"MIN_SCORE"`

	// Feature flags
	EnableSuggestions  bool `mapstructure:"ENABLE_SUGGESTIONS"`
	EnableAggregations bool `mapstructure:"ENABLE_AGGREGATIONS"`
	EnableSpellCheck   bool `mapstructure:"ENABLE_SPELL_CHECK"`
}

func DefaultConfig() *Config {
	return &Config{
		ElasticsearchURL:   "http://localhost:9200",
		DefaultLimit:       10,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		MinScore:           0.5,
		EnableSuggestions:  true,
		EnableAggregations: true,
		EnableSpellCheck:   true,
	}
}