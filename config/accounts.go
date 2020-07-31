package config

// Account represents a publisher account configuration
type Account struct {
	ID               string       `mapstructure:"id" json:"id"`
	Disabled         bool         `mapstructure:"disabled" json:"disabled"`
	PriceGranularity string       `mapstructure:"price_granularity" json:"price_granularity"`
	TTL              DefaultTTLs  `mapstructure:"ttl" json:"ttl"`
	Events           Events       `mapstructure:"events" json:"events"`
	CCPA             CCPA         `mapstructure:"ccpa" json:"ccpa"`
	GDPR             TCF2         `mapstructure:"gdpr" json:"gdpr"`
	Analytics        PubAnalytics `mapstructure:"analytics" json:"analytics"`
}

// PubAnalytics contains analytics settings for an account
type PubAnalytics struct {
}

// Events contains account-level controls for win/impression event support
type Events struct {
	Enabled bool `mapstructure:"enabled" json:"enabled"`
}

// UnknownAccount has settings that apply to unrecognized or missing accounts
var UnknownAccount = Account{
	ID: "", // pbsmetrics.PublisherUnknown,
	// global setting account_required should be moved here
	Disabled: true,
}
