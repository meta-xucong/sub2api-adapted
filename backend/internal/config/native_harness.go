package config

// NativeHarnessConfig is an opt-in, non-billing synthetic integration pilot.
// No ordinary provider/account selection or fallback is performed by this route.
type NativeHarnessConfig struct {
	// StandardAPI reserves /v1/chat/completions and /v1/models for the dedicated
	// allowlisted key. Default false; not an ordinary billed provider.
	StandardAPI bool `mapstructure:"standard_api"`
	// StandardWaitSeconds bounds standard-path observation of one accepted task.
	// Zero disables observation; no later call may grant new execution.
	StandardWaitSeconds int                           `mapstructure:"standard_wait_seconds"`
	Enabled             bool                          `mapstructure:"enabled"`
	AcceptingNew        bool                          `mapstructure:"accepting_new"`
	AllowedUserID       int64                         `mapstructure:"allowed_user_id"`
	AllowedAPIKeyID     int64                         `mapstructure:"allowed_api_key_id"`
	Routes              map[string]NativeHarnessRoute `mapstructure:"routes"`
}

type NativeHarnessRoute struct {
	// Empty preserves the historical synthetic-only route. Frozen with the operation.
	ExecutionMode  string `mapstructure:"execution_mode" json:"execution_mode,omitempty"`
	Origin         string `mapstructure:"origin" json:"origin"`
	CredentialEnv  string `mapstructure:"credential_env" json:"credential_env"`
	Source         string `mapstructure:"source" json:"source"`
	LedgerInstance string `mapstructure:"ledger_instance" json:"ledger_instance"`
	BindingID      string `mapstructure:"binding_id" json:"binding_id"`
	Revision       int    `mapstructure:"revision" json:"revision"`
	ModelAlias     string `mapstructure:"model_alias" json:"model_alias"`
	NativeModel    string `mapstructure:"native_model" json:"native_model"`
}
